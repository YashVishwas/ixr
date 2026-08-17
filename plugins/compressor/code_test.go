package compressor

import (
	"strings"
	"testing"
)

// --- looksLikeCode ---

func TestLooksLikeCode_DetectsCommonKeywords(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"go func", "func main() {\n\tfmt.Println(\"hi\")\n}"},
		{"python def", "def main():\n    print('hi')"},
		{"java class", "public class Main {\n}"},
		{"js import", "import React from 'react';\n"},
		{"c include", "#include <stdio.h>\nint main() {}"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !looksLikeCode(c.in) {
				t.Errorf("looksLikeCode(%q) = false, want true", c.in)
			}
		})
	}
}

func TestLooksLikeCode_HighBraceDensity(t *testing.T) {
	in := "{a}\n{b}\n{c}\n{d}"
	if !looksLikeCode(in) {
		t.Errorf("expected high brace-density content to look like code")
	}
}

func TestLooksLikeCode_RejectsProse(t *testing.T) {
	cases := []string{
		"# Project overview\n\nThis project does things.\n\n## Usage\n\nRun it.",
		"The quick brown fox jumps over the lazy dog. It was a nice day.",
		"-- this is a bullet, not SQL, and there are no code signals here at all",
	}
	for _, in := range cases {
		if looksLikeCode(in) {
			t.Errorf("looksLikeCode(%q) = true, want false (no code signals, low brace density)", in)
		}
	}
}

// --- stripComments ---

func TestStripComments_RemovesLineComments_ByteForByte(t *testing.T) {
	in := "func main() {\n\t// this explains nothing useful\n\tfmt.Println(\"hi\")\n}"
	got, ok := stripComments(in)
	if !ok {
		t.Fatalf("expected stripComments to report ok=true")
	}
	want := "func main() {\n\tfmt.Println(\"hi\")\n}"
	if got != want {
		t.Errorf("stripComments(%q) =\n%q\nwant\n%q", in, got, want)
	}
}

func TestStripComments_RemovesHashAndDashComments(t *testing.T) {
	in := "def main():\n    # a comment\n    print('hi')\n\n-- a sql comment\nSELECT 1;"
	got, ok := stripComments(in)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !containsAll(got, []string{"print('hi')", "SELECT 1;"}) {
		t.Errorf("expected real content preserved, got %q", got)
	}
	if containsAny(got, []string{"# a comment", "-- a sql comment"}) {
		t.Errorf("expected comments removed, got %q", got)
	}
}

func TestStripComments_RemovesSingleLineBlockComment(t *testing.T) {
	in := "func main() {\n\t/* single line block */\n\tdoWork()\n}"
	got, ok := stripComments(in)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if containsAny(got, []string{"/* single line block */"}) {
		t.Errorf("expected block comment removed, got %q", got)
	}
	if !containsAll(got, []string{"doWork()"}) {
		t.Errorf("expected real content preserved, got %q", got)
	}
}

func TestStripComments_RemovesMultiLineBlockComment(t *testing.T) {
	in := "func main() {\n\t/*\n\t * license header\n\t * more license text\n\t */\n\tdoWork()\n}"
	got, ok := stripComments(in)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if containsAny(got, []string{"license header", "license text"}) {
		t.Errorf("expected multi-line block comment removed, got %q", got)
	}
	if !containsAll(got, []string{"doWork()"}) {
		t.Errorf("expected real content preserved, got %q", got)
	}
}

func TestStripComments_NeverTouchesInlineComments(t *testing.T) {
	// A same-line trailing comment must survive untouched — stripping only
	// applies to lines that ARE (after trimming) a comment in full.
	in := "func main() {\n\tvisit(\"http://example.com\") // fetch the page\n}"
	got, ok := stripComments(in)
	// This content has no full-line comments at all — nothing to strip.
	if ok {
		t.Fatalf("expected ok=false: no full-line comments present, got %q", got)
	}
}

func TestStripComments_NeverTouchesImportLines(t *testing.T) {
	in := "import \"fmt\"\nimport \"os\"\n\nfunc main() {\n\t// noise\n\tfmt.Println(os.Args)\n}"
	got, ok := stripComments(in)
	if !ok {
		t.Fatalf("expected ok=true")
	}
	if !containsAll(got, []string{`import "fmt"`, `import "os"`}) {
		t.Errorf("expected import lines preserved, got %q", got)
	}
}

func TestStripComments_NotCodeShaped_ReturnsFalse(t *testing.T) {
	in := "# Project overview\n\nJust a description of the project, nothing more."
	if _, ok := stripComments(in); ok {
		t.Errorf("expected ok=false for non-code-shaped content")
	}
}

func TestStripComments_CodeShapedButNoComments_ReturnsFalse(t *testing.T) {
	in := "func main() {\n\tfmt.Println(\"hi\")\n}"
	if _, ok := stripComments(in); ok {
		t.Errorf("expected ok=false when code has no comments to strip")
	}
}

// --- integration through compress()/Intercept ---

func TestCompress_StripsCommentsFromCodeBeforeCollapsing(t *testing.T) {
	in := "func main() {\n\t// noise\n\tfmt.Println(\"hi\")\n}"
	got, omitted := compress(in, 1000)
	if omitted != 0 {
		t.Errorf("expected no truncation, got omitted=%d", omitted)
	}
	if containsAny(got, []string{"// noise"}) {
		t.Errorf("expected the comment stripped by compress(), got %q", got)
	}
}

func containsAll(s string, subs []string) bool {
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			return false
		}
	}
	return true
}

func containsAny(s string, subs []string) bool {
	for _, sub := range subs {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}
