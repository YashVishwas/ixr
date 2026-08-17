package compressor

import (
	"encoding/json"
	"sort"
	"strings"
)

// jsonArrayDedupMinEntries is the minimum array length before schema
// extraction is worth the overhead — below this, the compact-JSON fallback
// alone already captures most of the benefit and a "schema: a, b, c" header
// isn't worth the two extra lines it costs.
const jsonArrayDedupMinEntries = 3

// compressJSON is the structure-aware compression path: it understands JSON
// well enough to shrink it without a line-oriented heuristic, the way
// Headroom's SmartCrusher does for JSON tool output. It's deliberately much
// simpler than that — no schema inference beyond an exact-match check, no
// nested-array handling — a first real step past pure line-collapsing, not
// parity with a purpose-built JSON compressor.
//
// Two techniques, tried in order:
//  1. If content is a JSON array of objects that all share the exact same
//     key set, emit the key schema once followed by one compact value row
//     per object — the same information, without repeating every key on
//     every entry. Falls through to (2) if the objects' key sets differ at
//     all (a partial/best-effort merge risks silently dropping a field that
//     only appears on some entries, which this deliberately avoids).
//  2. Otherwise, re-marshal the parsed JSON compactly (no indentation/
//     whitespace) — safe for any valid JSON shape, no information lost,
//     works for a bare object, a scalar, or a heterogeneous array.
//
// Returns ok=false when content isn't valid JSON at all, or when neither
// technique actually produced anything smaller than the input — compress
// falls back to the line-oriented heuristic in either case.
func compressJSON(content string) (string, bool) {
	var data any
	if err := json.Unmarshal([]byte(content), &data); err != nil {
		return "", false
	}

	result := ""
	if arr, ok := data.([]any); ok && len(arr) >= jsonArrayDedupMinEntries {
		if keys, ok := commonObjectSchema(arr); ok {
			result = renderSchemaForm(keys, arr)
		}
	}
	if result == "" {
		compact, err := json.Marshal(data)
		if err != nil {
			return "", false
		}
		result = string(compact)
	}

	if len(result) >= len(content) {
		return "", false
	}
	return result, true
}

// commonObjectSchema returns the sorted key set shared by every element of
// arr, only when every element is a JSON object with exactly that key set —
// a strict match, not a union or intersection, so schema-form rendering
// never silently drops a field that only some entries have.
func commonObjectSchema(arr []any) ([]string, bool) {
	first, ok := arr[0].(map[string]any)
	if !ok {
		return nil, false
	}
	keys := make([]string, 0, len(first))
	for k := range first {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, item := range arr[1:] {
		obj, ok := item.(map[string]any)
		if !ok || len(obj) != len(keys) {
			return nil, false
		}
		for _, k := range keys {
			if _, exists := obj[k]; !exists {
				return nil, false
			}
		}
	}
	return keys, true
}

// renderSchemaForm writes the key schema once, then one compact value row
// per array element in schema-key order — e.g.:
//
//	schema: id, name, status
//	[1, "alice", "active"]
//	[2, "bob", "inactive"]
func renderSchemaForm(keys []string, arr []any) string {
	var b strings.Builder
	b.WriteString("schema: ")
	b.WriteString(strings.Join(keys, ", "))
	for _, item := range arr {
		obj := item.(map[string]any) // safe: commonObjectSchema already validated every element
		vals := make([]string, len(keys))
		for i, k := range keys {
			v, err := json.Marshal(obj[k])
			if err != nil {
				vals[i] = "null"
				continue
			}
			vals[i] = string(v)
		}
		b.WriteByte('\n')
		b.WriteByte('[')
		b.WriteString(strings.Join(vals, ", "))
		b.WriteByte(']')
	}
	return b.String()
}
