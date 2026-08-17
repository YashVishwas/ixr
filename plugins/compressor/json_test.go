package compressor

import "testing"

func TestCompressJSON_ArrayOfMatchingObjects_UsesSchemaForm(t *testing.T) {
	in := `[{"id":1,"name":"alice","status":"active"},{"id":2,"name":"bob","status":"inactive"},{"id":3,"name":"carol","status":"active"}]`
	got, ok := compressJSON(in)
	if !ok {
		t.Fatalf("expected compressJSON to succeed on a matching-schema array")
	}
	want := "schema: id, name, status\n" +
		`[1, "alice", "active"]` + "\n" +
		`[2, "bob", "inactive"]` + "\n" +
		`[3, "carol", "active"]`
	if got != want {
		t.Errorf("compressJSON(%q) =\n%q\nwant\n%q", in, got, want)
	}
}

func TestCompressJSON_ArrayBelowDedupMinimum_FallsBackToCompact(t *testing.T) {
	// Only 2 entries — below jsonArrayDedupMinEntries (3), so schema-form
	// isn't worth the header overhead; falls back to compact JSON.
	in := `[{"id": 1, "name": "alice"}, {"id": 2, "name": "bob"}]`
	got, ok := compressJSON(in)
	if !ok {
		t.Fatalf("expected compressJSON to succeed (compact fallback)")
	}
	want := `[{"id":1,"name":"alice"},{"id":2,"name":"bob"}]`
	if got != want {
		t.Errorf("compressJSON(%q) = %q, want %q", in, got, want)
	}
}

func TestCompressJSON_MismatchedSchema_FallsBackToCompact(t *testing.T) {
	// Third entry has a different key set (no "status") — schema-form would
	// either drop that entry's uniqueness or silently omit "status" for the
	// others; instead this must fall back to compact JSON, which loses
	// nothing. Pretty-printed input so the compact fallback actually has
	// whitespace to remove — an already-compact input would trip the "no
	// benefit" guard and return ok=false, which isn't what this test is for.
	in := `[
  {"id": 1, "name": "alice", "status": "active"},
  {"id": 2, "name": "bob", "status": "inactive"},
  {"id": 3, "name": "carol"}
]`
	got, ok := compressJSON(in)
	if !ok {
		t.Fatalf("expected compressJSON to succeed (compact fallback)")
	}
	want := `[{"id":1,"name":"alice","status":"active"},{"id":2,"name":"bob","status":"inactive"},{"id":3,"name":"carol"}]`
	if got != want {
		t.Errorf("compressJSON(%q) = %q, want %q", in, got, want)
	}
}

func TestCompressJSON_ArrayOfNonObjects_FallsBackToCompact(t *testing.T) {
	in := `["alice", "bob", "carol", "dave"]`
	got, ok := compressJSON(in)
	if !ok {
		t.Fatalf("expected compressJSON to succeed (compact fallback)")
	}
	want := `["alice","bob","carol","dave"]`
	if got != want {
		t.Errorf("compressJSON(%q) = %q, want %q", in, got, want)
	}
}

func TestCompressJSON_SingleObject_CompactedOnly(t *testing.T) {
	in := `{
  "id": 1,
  "name": "alice"
}`
	got, ok := compressJSON(in)
	if !ok {
		t.Fatalf("expected compressJSON to succeed")
	}
	want := `{"id":1,"name":"alice"}`
	if got != want {
		t.Errorf("compressJSON(%q) = %q, want %q", in, got, want)
	}
}

func TestCompressJSON_InvalidJSON_ReturnsFalse(t *testing.T) {
	cases := []string{"", "not json at all", `{"unterminated": `, "error: timeout\nerror: timeout\n"}
	for _, in := range cases {
		if _, ok := compressJSON(in); ok {
			t.Errorf("compressJSON(%q): expected ok=false for invalid JSON", in)
		}
	}
}

func TestCompressJSON_AlreadyCompact_NoLargerResult_ReturnsFalse(t *testing.T) {
	// A bare scalar re-marshals to itself — never larger, but also never
	// smaller, so this should report ok=false (nothing gained) rather than
	// claim a "compression" that didn't actually shrink anything.
	in := `42`
	if _, ok := compressJSON(in); ok {
		t.Errorf("compressJSON(%q): expected ok=false when the result isn't smaller than the input", in)
	}
}

// --- commonObjectSchema ---

func TestCommonObjectSchema_MatchingKeys_ReturnsSortedKeys(t *testing.T) {
	arr := []any{
		map[string]any{"b": 1, "a": 2},
		map[string]any{"a": 3, "b": 4},
	}
	keys, ok := commonObjectSchema(arr)
	if !ok {
		t.Fatalf("expected ok=true for matching key sets")
	}
	want := []string{"a", "b"}
	if len(keys) != len(want) || keys[0] != want[0] || keys[1] != want[1] {
		t.Errorf("commonObjectSchema = %v, want %v (sorted)", keys, want)
	}
}

func TestCommonObjectSchema_FirstElementNotObject_ReturnsFalse(t *testing.T) {
	arr := []any{"not an object", map[string]any{"a": 1}}
	if _, ok := commonObjectSchema(arr); ok {
		t.Errorf("expected ok=false when the first element isn't an object")
	}
}

func TestCommonObjectSchema_DifferentKeyCount_ReturnsFalse(t *testing.T) {
	arr := []any{
		map[string]any{"a": 1, "b": 2},
		map[string]any{"a": 1},
	}
	if _, ok := commonObjectSchema(arr); ok {
		t.Errorf("expected ok=false when key counts differ")
	}
}

func TestCommonObjectSchema_SameCountDifferentKeys_ReturnsFalse(t *testing.T) {
	arr := []any{
		map[string]any{"a": 1, "b": 2},
		map[string]any{"a": 1, "c": 2},
	}
	if _, ok := commonObjectSchema(arr); ok {
		t.Errorf("expected ok=false when key names differ despite matching count")
	}
}

// --- renderSchemaForm ---

func TestRenderSchemaForm_ByteForByte(t *testing.T) {
	keys := []string{"id", "name"}
	arr := []any{
		map[string]any{"id": float64(1), "name": "alice"},
		map[string]any{"id": float64(2), "name": "bob"},
	}
	got := renderSchemaForm(keys, arr)
	want := "schema: id, name\n[1, \"alice\"]\n[2, \"bob\"]"
	if got != want {
		t.Errorf("renderSchemaForm =\n%q\nwant\n%q", got, want)
	}
}
