// Package session manages per-request conversation history across HTTP calls.
// The client supplies an X-IXR-Session-ID header; the gateway prepends stored
// history to each request and appends the new turn after the response arrives.
package session

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

// SessionStore manages per-session conversation history keyed by a tenant-scoped ID.
type SessionStore interface {
	// Get returns the stored message history for key. Returns false when the
	// session does not exist or has expired.
	Get(ctx context.Context, key string) ([]schema.Message, bool)
	// Append adds a user+assistant turn pair to the session, creating it if absent.
	// Trims the oldest turns when the total exceeds maxTurns.
	Append(ctx context.Context, key string, user schema.Message, assistant schema.Message)
	// Delete removes a session explicitly (used by X-IXR-Session-Reset).
	Delete(ctx context.Context, key string)
}

type sessionEntry struct {
	messages  []schema.Message
	expiresAt time.Time
}

// MemorySessionStore is an in-process session store with TTL eviction.
// Safe for concurrent use.
type MemorySessionStore struct {
	mu       sync.Mutex
	sessions map[string]*sessionEntry
	ttl      time.Duration
	maxTurns int
	done     chan struct{}
}

// NewMemorySessionStore creates a session store.
// ttl=0 means sessions never expire. maxTurns=0 means no turn limit.
func NewMemorySessionStore(ttl time.Duration, maxTurns int) *MemorySessionStore {
	s := &MemorySessionStore{
		sessions: make(map[string]*sessionEntry),
		ttl:      ttl,
		maxTurns: maxTurns,
		done:     make(chan struct{}),
	}
	if ttl > 0 {
		go s.gcLoop()
	}
	return s
}

func (s *MemorySessionStore) Get(_ context.Context, key string) ([]schema.Message, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[key]
	if !ok {
		return nil, false
	}
	if !e.expiresAt.IsZero() && time.Now().After(e.expiresAt) {
		delete(s.sessions, key)
		return nil, false
	}
	out := make([]schema.Message, len(e.messages))
	copy(out, e.messages)
	return out, true
}

func (s *MemorySessionStore) Append(_ context.Context, key string, user schema.Message, assistant schema.Message) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.sessions[key]
	if !ok {
		e = &sessionEntry{}
		s.sessions[key] = e
	}
	e.messages = append(e.messages, user, assistant)
	if s.maxTurns > 0 {
		max := s.maxTurns * 2
		if len(e.messages) > max {
			e.messages = e.messages[len(e.messages)-max:]
		}
	}
	if s.ttl > 0 {
		e.expiresAt = time.Now().Add(s.ttl)
	}
}

func (s *MemorySessionStore) Delete(_ context.Context, key string) {
	s.mu.Lock()
	delete(s.sessions, key)
	s.mu.Unlock()
}

// Close stops the background GC goroutine.
func (s *MemorySessionStore) Close() {
	close(s.done)
}

func (s *MemorySessionStore) gcLoop() {
	interval := s.ttl / 2
	if interval < time.Second {
		interval = time.Second
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			s.mu.Lock()
			now := time.Now()
			for k, e := range s.sessions {
				if !e.expiresAt.IsZero() && now.After(e.expiresAt) {
					delete(s.sessions, k)
				}
			}
			s.mu.Unlock()
		case <-s.done:
			return
		}
	}
}

// deltaEntry is one line in the on-disk session journal.
// At runtime each line holds 2 messages (the new user+assistant pair).
// After compaction each line holds the full accumulated history for a session.
type deltaEntry struct {
	Key       string           `json:"key"`
	Msgs      []schema.Message `json:"msgs"`
	ExpiresAt time.Time        `json:"expires_at,omitempty"`
}

// liveSession is used during replay and compaction.
type liveSession struct {
	msgs      []schema.Message
	expiresAt time.Time
}

// PersistentSessionStore wraps MemorySessionStore with an append-only delta journal.
// Each Append writes exactly one line (2 messages) regardless of session length.
// On startup, deltas are accumulated per key and expired sessions are pruned.
// Compaction rewrites the file with one line per surviving session (temp+rename).
// dir="" falls back to pure in-memory operation.
type PersistentSessionStore struct {
	*MemorySessionStore
	fileMu sync.Mutex // guards journal file writes only
	file   *os.File
}

// NewPersistentSessionStore loads dir/sessions.jsonl into memory then opens it
// for appending. dir="" returns a plain in-memory store.
func NewPersistentSessionStore(ttl time.Duration, maxTurns int, dir string) *PersistentSessionStore {
	ps := &PersistentSessionStore{
		MemorySessionStore: NewMemorySessionStore(ttl, maxTurns),
	}
	if dir == "" {
		return ps
	}
	path := filepath.Join(dir, "sessions.jsonl")
	ps.replayJournal(path)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Warn("session store: journal unavailable, running in-memory only", "path", path, "err", err)
		return ps
	}
	ps.file = f
	return ps
}

func (ps *PersistentSessionStore) replayJournal(path string) {
	f, err := os.Open(path)
	if err != nil {
		return // first run — file doesn't exist yet
	}
	defer f.Close()

	now := time.Now()
	acc := make(map[string]*liveSession)
	var totalLines int

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	for scanner.Scan() {
		totalLines++
		var e deltaEntry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		a, ok := acc[e.Key]
		if !ok {
			a = &liveSession{}
			acc[e.Key] = a
		}
		a.msgs = append(a.msgs, e.Msgs...)
		a.expiresAt = e.ExpiresAt // last entry's expires_at is the effective session expiry
	}

	// Load non-expired sessions into memory.
	live := make(map[string]*liveSession)
	for key, a := range acc {
		if !a.expiresAt.IsZero() && now.After(a.expiresAt) {
			continue // expired
		}
		msgs := a.msgs
		if ps.maxTurns > 0 {
			max := ps.maxTurns * 2
			if len(msgs) > max {
				msgs = msgs[len(msgs)-max:]
			}
		}
		ps.MemorySessionStore.mu.Lock()
		ps.MemorySessionStore.sessions[key] = &sessionEntry{
			messages:  msgs,
			expiresAt: a.expiresAt,
		}
		ps.MemorySessionStore.mu.Unlock()
		live[key] = &liveSession{msgs: msgs, expiresAt: a.expiresAt}
	}

	if len(live) > 0 {
		slog.Info("session store: replayed journal", "sessions", len(live), "path", path)
	}
	// Compact when the file has more lines than surviving sessions (stale deltas or expired keys).
	if totalLines > len(live) {
		ps.compactJournal(path, live)
	}
}

func (ps *PersistentSessionStore) compactJournal(path string, live map[string]*liveSession) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for key, a := range live {
		line, err := json.Marshal(deltaEntry{Key: key, Msgs: a.msgs, ExpiresAt: a.expiresAt})
		if err != nil {
			continue
		}
		_, _ = w.Write(line)
		_ = w.WriteByte('\n')
	}
	if err := w.Flush(); err != nil {
		f.Close()
		os.Remove(tmp)
		return
	}
	f.Close()
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return
	}
	slog.Info("session store: compacted journal", "sessions", len(live), "path", path)
}

// Append writes the new turn to memory and appends a 2-message delta line to the journal.
func (ps *PersistentSessionStore) Append(ctx context.Context, key string, user schema.Message, assistant schema.Message) {
	ps.MemorySessionStore.Append(ctx, key, user, assistant)
	if ps.file == nil {
		return
	}
	var exp time.Time
	if ps.ttl > 0 {
		exp = time.Now().Add(ps.ttl)
	}
	line, err := json.Marshal(deltaEntry{
		Key:       key,
		Msgs:      []schema.Message{user, assistant},
		ExpiresAt: exp,
	})
	if err != nil {
		return
	}
	ps.fileMu.Lock()
	_, _ = ps.file.Write(append(line, '\n'))
	ps.fileMu.Unlock()
}

// Close flushes and closes the journal file and stops the GC goroutine.
func (ps *PersistentSessionStore) Close() error {
	ps.MemorySessionStore.Close()
	if ps.file != nil {
		return ps.file.Close()
	}
	return nil
}
