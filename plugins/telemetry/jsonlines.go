package telemetry

import (
	"context"
	"encoding/json"
	"io"
	"sync"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// JSONLinesSink writes one TelemetryRecord per line as JSON to an io.Writer.
// Safe for concurrent use.
type JSONLinesSink struct {
	mu  sync.Mutex
	enc *json.Encoder
}

// NewJSONLinesSink wraps w in a sink that writes JSON lines.
func NewJSONLinesSink(w io.Writer) *JSONLinesSink {
	return &JSONLinesSink{enc: json.NewEncoder(w)}
}

// Write marshals rec as a JSON line.
func (s *JSONLinesSink) Write(_ context.Context, rec schema.TelemetryRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.enc.Encode(rec)
}
