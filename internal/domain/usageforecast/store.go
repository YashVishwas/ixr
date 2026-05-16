package usageforecast

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// Store records token usage observations and returns history for forecast windows.
type Store interface {
	Add(ctx context.Context, obs Observation) error
	Query(ctx context.Context, q Query) ([]Observation, error)
}

// Observation is one completed model call's token usage.
type Observation struct {
	Timestamp time.Time
	UserID    string
	Model     string
	TokensIn  int
	TokensOut int
}

// TotalTokens returns the full token consumption for an observation.
func (o Observation) TotalTokens() int {
	return o.TokensIn + o.TokensOut
}

// Query filters usage history.
type Query struct {
	UserID string
	Model  string
	Since  time.Time
	Until  time.Time
}

// MemoryStore is an in-process usage history store.
type MemoryStore struct {
	mu   sync.RWMutex
	data []Observation
}

// NewMemoryStore creates an empty in-memory usage store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{}
}

// Add records a usage observation.
func (s *MemoryStore) Add(_ context.Context, obs Observation) error {
	if obs.Timestamp.IsZero() {
		obs.Timestamp = time.Now()
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data = append(s.data, obs)
	return nil
}

// Query returns observations matching q, sorted oldest first.
func (s *MemoryStore) Query(_ context.Context, q Query) ([]Observation, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	out := make([]Observation, 0, len(s.data))
	for _, obs := range s.data {
		if q.UserID != "" && obs.UserID != q.UserID {
			continue
		}
		if q.Model != "" && obs.Model != q.Model {
			continue
		}
		if !q.Since.IsZero() && obs.Timestamp.Before(q.Since) {
			continue
		}
		if !q.Until.IsZero() && !obs.Timestamp.Before(q.Until) {
			continue
		}
		out = append(out, obs)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].Timestamp.Before(out[j].Timestamp)
	})
	return out, nil
}

// Recorder subscribes to CallEvents and stores completed token usage.
type Recorder struct {
	store Store
}

// NewRecorder creates a bus consumer that captures token usage.
func NewRecorder(store Store) *Recorder {
	return &Recorder{store: store}
}

func (r *Recorder) Name() string { return "usage-forecast-recorder" }

// OnEvent stores successful call usage. Events without a user ID are ignored
// because forecasts are user-scoped.
func (r *Recorder) OnEvent(ctx context.Context, ev *schema.CallEvent) error {
	if ev == nil || ev.UserID == "" || ev.Error != "" {
		return nil
	}
	total := ev.TokensIn + ev.TokensOut
	if total == 0 {
		total = ev.Response.Usage.TotalTokens
	}
	if total == 0 {
		return nil
	}
	return r.store.Add(ctx, Observation{
		Timestamp: ev.Timestamp,
		UserID:    ev.UserID,
		Model:     ev.Model,
		TokensIn:  ev.TokensIn,
		TokensOut: total - ev.TokensIn,
	})
}
