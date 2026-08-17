// Package feedback resolves a caller's after-the-fact rating of a response
// (POST /v1/feedback) back to the model that produced it, so the rating can
// train the same bandit RFC Gap 12's auto-routing already updates from
// success/latency (plugins/banditreward). FinishReason-derived quality is
// free but coarse — it can't tell a merely complete answer from a genuinely
// good one. A caller's own thumbs up/down is the real signal; this package
// is the plumbing that makes it usable without the caller having to know
// anything about models, routing, or bandits — just the response ID they
// already got back.
package feedback

import (
	"sync"
	"time"
)

// CallInfo is what's needed to turn a feedback submission into a bandit
// update: which arm to credit, and whether that arm was even chosen by the
// bandit in the first place.
type CallInfo struct {
	Model      string
	AutoRouted bool
}

// Store maps a CallEvent ID to the CallInfo needed to resolve later
// feedback, with TTL eviction — a rating submitted long after the response
// is gone (client crashed, browser tab closed for days) shouldn't pin
// memory forever. In-memory only, single-instance, matching
// internal/domain/retrieval.Store's default: feedback is expected within
// the same session that received the response, not indefinitely.
type Store struct {
	mu      sync.Mutex
	entries map[string]entry
	maxSize int
	ttl     time.Duration
	now     func() time.Time
}

type entry struct {
	info      CallInfo
	expiresAt time.Time
}

// NewStore creates a Store. maxSize<=0 means unlimited; ttl<=0 means
// entries never expire (still subject to maxSize eviction).
func NewStore(maxSize int, ttl time.Duration) *Store {
	return &Store{entries: make(map[string]entry), maxSize: maxSize, ttl: ttl, now: time.Now}
}

// Record indexes a call so a later feedback submission referencing callID
// can be resolved. Called for every non-shadow CallEvent — see
// plugins/feedback, which does the indexing from the event bus.
func (s *Store) Record(callID string, info CallInfo) {
	if callID == "" {
		return // nothing to index a rating against later
	}
	var exp time.Time
	if s.ttl > 0 {
		exp = s.now().Add(s.ttl)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.maxSize > 0 && len(s.entries) >= s.maxSize {
		s.evictOldestLocked()
	}
	s.entries[callID] = entry{info: info, expiresAt: exp}
}

// Lookup returns the CallInfo indexed under callID, or ok=false if it was
// never recorded, has expired, or has been evicted.
func (s *Store) Lookup(callID string) (CallInfo, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[callID]
	if !ok {
		return CallInfo{}, false
	}
	if !e.expiresAt.IsZero() && s.now().After(e.expiresAt) {
		delete(s.entries, callID)
		return CallInfo{}, false
	}
	return e.info, true
}

// Len returns the current entry count, for monitoring/tests.
func (s *Store) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// evictOldestLocked drops one entry to make room. Map iteration order is
// randomized, not insertion order, so unlike retrieval.Store this doesn't
// try to find the single oldest entry — any entry is an equally reasonable
// thing to drop to make room under maxSize, and a random evict is O(1)
// instead of the O(n) scan a real LRU/oldest-first policy would need. Bad
// luck evicts a call still worth crediting occasionally; that's an
// acceptable trade for a best-effort feedback channel, not a correctness
// requirement (a caller whose feedback lands on an evicted entry just gets
// a 404 and their rating doesn't count — never a wrong or corrupted one).
func (s *Store) evictOldestLocked() {
	for id := range s.entries {
		delete(s.entries, id)
		return
	}
}
