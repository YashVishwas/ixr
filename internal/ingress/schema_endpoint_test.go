package ingress

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

// TestSchemaJSONMatchesCheckedInFile guards against the exact drift that
// motivated this test: ixrJSONSchema's "$defs" (served live at GET
// /v1/schema) and schema/ixr.schema.json (the file published for non-Go
// consumers, referenced by the main README) had independently gone stale
// against pkg/schema and against each other — each missing types and fields
// the other had. There is no build-time link between them (schema/ isn't
// reachable from this package via go:embed), so this test is the only thing
// that would catch them drifting apart again.
func TestSchemaJSONMatchesCheckedInFile(t *testing.T) {
	liveBytes, err := json.Marshal(ixrJSONSchemaDefs)
	if err != nil {
		t.Fatalf("marshal live $defs: %v", err)
	}
	var live map[string]any
	if err := json.Unmarshal(liveBytes, &live); err != nil {
		t.Fatalf("re-unmarshal live $defs: %v", err)
	}

	fileBytes, err := os.ReadFile("../../schema/ixr.schema.json")
	if err != nil {
		t.Fatalf("read schema/ixr.schema.json: %v", err)
	}
	var checkedIn struct {
		Defs map[string]any `json:"$defs"`
	}
	if err := json.Unmarshal(fileBytes, &checkedIn); err != nil {
		t.Fatalf("unmarshal schema/ixr.schema.json: %v", err)
	}

	if !reflect.DeepEqual(live, checkedIn.Defs) {
		liveJSON, _ := json.MarshalIndent(live, "", "  ")
		fileJSON, _ := json.MarshalIndent(checkedIn.Defs, "", "  ")
		t.Errorf("ixrJSONSchemaDefs (schema_endpoint.go) has drifted from schema/ixr.schema.json's $defs — "+
			"update whichever one changed to match the other.\n\nlive:\n%s\n\ncheckedIn:\n%s", liveJSON, fileJSON)
	}
}

func TestSchemaHandler_ServesLiveSchema(t *testing.T) {
	h := NewSchemaHandler()
	req := httptest.NewRequest("GET", "/v1/schema", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	if w.Code != 200 {
		t.Fatalf("status: got %d, want 200", w.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("response body is not valid JSON: %v", err)
	}
	if _, ok := body["$defs"]; !ok {
		t.Fatal("response missing $defs")
	}
}
