package compressor

import "strings"

// codeSignals are keyword phrases common enough across mainstream languages
// that finding any one of them is a reasonable signal the content is code,
// not prose — used to gate comment-stripping so a markdown "# Heading" or a
// prose line that happens to start with "--" doesn't get mangled.
var codeSignals = []string{
	"func ", "def ", "class ", "import ", "package ", "public ", "private ",
	"function ", "const ", "let ", "var ", "#include", "using ",
}

// looksLikeCode is a deliberately cheap heuristic, not a language detector:
// any common code keyword, or a brace density too high for ordinary prose.
// False positives (treating prose as code) are the risk to avoid here —
// stripComments only ever removes whole comment lines, so a false positive
// at worst strips nothing (no comment-marker lines present) rather than
// corrupting content, but looksLikeCode still exists to avoid even
// attempting the pass on content it has no business touching.
func looksLikeCode(content string) bool {
	for _, sig := range codeSignals {
		if strings.Contains(content, sig) {
			return true
		}
	}
	braces := strings.Count(content, "{") + strings.Count(content, "}")
	if braces == 0 {
		return false
	}
	lines := strings.Count(content, "\n") + 1
	return braces*4 >= lines
}

// stripComments removes full-line comments from code-shaped content:
// // # -- line comments, and /* */ block comments (including multi-line —
// tracked with a simple boolean, not full lexing). It never touches inline
// trailing comments (stripping "// foo" out of a line containing
// "http://example.com" would be a false-positive risk not worth taking) —
// only lines that, after trimming leading whitespace, start with a comment
// marker are eligible. Import/using/package lines are never touched; they
// carry real dependency information, not filler.
//
// Known limitation: a shebang line (#!/usr/bin/env python) matches the "#"
// comment marker and gets stripped along with real comments — losing the
// interpreter hint. Accepted for v1 as a low-probability, low-severity edge
// case rather than special-cased.
//
// Returns ok=false when content doesn't look like code at all, or looks
// like code but had no comment lines to strip — callers should fall back to
// the plain collapsing pipeline in either case.
func stripComments(content string) (result string, ok bool) {
	if !looksLikeCode(content) {
		return "", false
	}

	lines := strings.Split(content, "\n")
	out := make([]string, 0, len(lines))
	stripped := false
	inBlock := false

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if inBlock {
			stripped = true
			if strings.HasSuffix(trimmed, "*/") {
				inBlock = false
			}
			continue
		}

		switch {
		case isLineComment(trimmed):
			stripped = true
			continue
		case strings.HasPrefix(trimmed, "/*"):
			stripped = true
			if !strings.HasSuffix(trimmed, "*/") || len(trimmed) < 4 {
				inBlock = true
			}
			continue
		}

		out = append(out, line)
	}

	if !stripped {
		return "", false
	}
	return strings.Join(out, "\n"), true
}

// isLineComment reports whether a trimmed line is entirely a single-line
// comment: //, #, or -- (C-family, Python/Ruby/shell, SQL/Lua respectively).
func isLineComment(trimmed string) bool {
	return strings.HasPrefix(trimmed, "//") ||
		strings.HasPrefix(trimmed, "#") ||
		strings.HasPrefix(trimmed, "--")
}
