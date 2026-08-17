package retrieval

import (
	"strings"
	"testing"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func TestTool_HasExpectedNameAndRequiredIDParam(t *testing.T) {
	tool := Tool()
	if tool.Function.Name != ToolName {
		t.Errorf("Name = %q, want %q", tool.Function.Name, ToolName)
	}
	props, ok := tool.Function.Parameters["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %+v", tool.Function.Parameters)
	}
	if _, ok := props["id"]; !ok {
		t.Errorf("expected an \"id\" property, got %+v", props)
	}
	required, ok := tool.Function.Parameters["required"].([]string)
	if !ok || len(required) != 1 || required[0] != "id" {
		t.Errorf("expected required=[\"id\"], got %+v", tool.Function.Parameters["required"])
	}
}

func TestMarker_ContainsIDAndToolName(t *testing.T) {
	m := Marker("ret_42", 1500)
	if !strings.Contains(m, "ret_42") {
		t.Errorf("marker should contain the id, got %q", m)
	}
	if !strings.Contains(m, ToolName) {
		t.Errorf("marker should contain the tool name, got %q", m)
	}
	if !strings.Contains(m, "1500") {
		t.Errorf("marker should contain the omitted char count, got %q", m)
	}
}

func TestParseArgs_ValidJSON(t *testing.T) {
	id, ok := ParseArgs(`{"id":"ret_7"}`)
	if !ok || id != "ret_7" {
		t.Errorf("ParseArgs = (%q, %v), want (\"ret_7\", true)", id, ok)
	}
}

func TestParseArgs_MissingID(t *testing.T) {
	if _, ok := ParseArgs(`{}`); ok {
		t.Error("expected ok=false when id is missing")
	}
}

func TestParseArgs_MalformedJSON(t *testing.T) {
	if _, ok := ParseArgs(`not json`); ok {
		t.Error("expected ok=false for malformed JSON")
	}
}

func TestFindCall_FindsByName(t *testing.T) {
	calls := []schema.ToolCall{
		{ID: "c1", Function: schema.ToolFunction{Name: "get_weather"}},
		{ID: "c2", Function: schema.ToolFunction{Name: ToolName, Arguments: `{"id":"ret_1"}`}},
	}
	got, ok := FindCall(calls)
	if !ok || got.ID != "c2" {
		t.Errorf("FindCall = (%+v, %v), want the ixr_retrieve call", got, ok)
	}
}

func TestFindCall_NoneMatches(t *testing.T) {
	calls := []schema.ToolCall{
		{ID: "c1", Function: schema.ToolFunction{Name: "get_weather"}},
	}
	if _, ok := FindCall(calls); ok {
		t.Error("expected ok=false when no call matches ToolName")
	}
}
