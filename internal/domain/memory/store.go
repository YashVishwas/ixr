// Package memory provides bounded, short-lived user-level fact storage for
// ixr. Facts are extracted from conversation turns and stored keyed by
// userKey (tenantID:userID). On new sessions, relevant memories are
// retrieved and injected as context so the model remembers the user for as
// long as those facts stay within the store's TTL — this is intentionally
// not indefinite persistent memory; see MemoryStore's TTL/maxPerUser bounds.
package memory

import (
	"bufio"
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Entry is one stored fact about a user.
type Entry struct {
	ID        string    `json:"id"`
	UserKey   string    `json:"user_key"` // tenantID:userID
	Category  string    `json:"category"` // name | employer | project | location | role | preference | other
	Content   string    `json:"content"`  // "User's name is Arun"
	Source    string    `json:"source"`   // "rule" | "llm"
	CreatedAt time.Time `json:"created_at"`
}

// Store persists and retrieves user memory entries.
type Store interface {
	// Save adds or updates a memory entry. If an entry with the same
	// UserKey+Category+Source already exists, a new entry is appended
	// (timestamp-based history — callers retrieve the most recent per category).
	Save(ctx context.Context, e Entry) error
	// Recent returns up to n entries for userKey, most recent first.
	Recent(ctx context.Context, userKey string, n int) ([]Entry, error)
	// All returns every entry for userKey.
	All(ctx context.Context, userKey string) ([]Entry, error)
}

// Defaults applied by NewMemoryStore. Without some bound, entries accumulate
// forever — both the in-process map and the on-disk journal grow without
// limit as long as a user keeps talking. ttl keeps memory relevant for
// roughly a session's worth of time rather than persisting indefinitely;
// maxPerUser is a hard backstop against runaway growth within that window.
// Use NewMemoryStoreWithLimits to override either.
const (
	defaultMemoryTTL         = time.Hour
	defaultMaxEntriesPerUser = 50
)

// defaultCompactInterval is how often a persistent store rewrites its
// journal to drop expired/over-cap lines while running, so the file doesn't
// grow unbounded between restarts the way it used to (compaction previously
// only ran once, at startup).
const defaultCompactInterval = 15 * time.Minute

// MemoryStore is an in-process store backed by an optional append-only journal.
// Safe for concurrent use. Entries older than ttl are treated as expired and
// dropped; each user's entry list is capped at maxPerUser regardless of age.
type MemoryStore struct {
	mu         sync.Mutex
	entries    map[string][]Entry // userKey → ordered list (oldest first)
	file       *os.File
	filePath   string
	fileMu     sync.Mutex
	ttl        time.Duration
	maxPerUser int

	stopCompact chan struct{}
	compactDone chan struct{}
}

// NewMemoryStore creates a store with the default bounds (1 hour TTL, 50
// entries per user, journal recompacted every 15 minutes if persistent).
// dir="" means in-memory only.
func NewMemoryStore(dir string) *MemoryStore {
	return NewMemoryStoreWithLimits(dir, defaultMemoryTTL, defaultMaxEntriesPerUser, defaultCompactInterval)
}

// NewMemoryStoreWithLimits creates a store whose entries expire after ttl
// (<=0 means entries never expire) and whose per-user entry list is capped
// at maxPerUser (<=0 means unbounded). dir="" means in-memory only.
//
// When dir is set and compactInterval > 0, the journal is periodically
// rewritten in the background to drop expired/over-cap lines — otherwise a
// long-running process would still grow the file forever even though
// in-memory usage stays bounded, since appendJournal writes every Save
// unconditionally. compactInterval <= 0 disables background compaction;
// the journal is still compacted once on startup during replay.
func NewMemoryStoreWithLimits(dir string, ttl time.Duration, maxPerUser int, compactInterval time.Duration) *MemoryStore {
	s := &MemoryStore{entries: make(map[string][]Entry), ttl: ttl, maxPerUser: maxPerUser}
	if dir != "" {
		s.filePath = filepath.Join(dir, "memory.jsonl")
		s.replayJournal(s.filePath)
		if compactInterval > 0 {
			s.startPeriodicCompaction(compactInterval)
		}
	}
	return s
}

func (s *MemoryStore) Save(_ context.Context, e Entry) error {
	if e.CreatedAt.IsZero() {
		e.CreatedAt = time.Now()
	}
	if e.ID == "" {
		e.ID = e.UserKey + ":" + e.Category + ":" + e.CreatedAt.Format(time.RFC3339Nano)
	}

	s.mu.Lock()
	s.entries[e.UserKey] = s.pruneLocked(append(s.entries[e.UserKey], e))
	s.mu.Unlock()

	s.appendJournal(e)
	return nil
}

func (s *MemoryStore) Recent(_ context.Context, userKey string, n int) ([]Entry, error) {
	s.mu.Lock()
	all := s.pruneLocked(s.entries[userKey])
	s.entries[userKey] = all
	all = append([]Entry(nil), all...)
	s.mu.Unlock()

	// Sort descending by CreatedAt.
	sort.Slice(all, func(i, j int) bool {
		return all[i].CreatedAt.After(all[j].CreatedAt)
	})

	// Return at most the n most recent entries, deduplicated by category
	// (keep only the most recent entry per category).
	seen := make(map[string]bool)
	var result []Entry
	for _, e := range all {
		if seen[e.Category] {
			continue
		}
		seen[e.Category] = true
		result = append(result, e)
		if len(result) >= n {
			break
		}
	}
	return result, nil
}

func (s *MemoryStore) All(_ context.Context, userKey string) ([]Entry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	all := s.pruneLocked(s.entries[userKey])
	s.entries[userKey] = all
	return append([]Entry(nil), all...), nil
}

// pruneLocked drops expired entries and trims to maxPerUser (oldest first).
// Callers must hold s.mu.
func (s *MemoryStore) pruneLocked(entries []Entry) []Entry {
	if s.ttl > 0 {
		now := time.Now()
		live := entries[:0:0]
		for _, e := range entries {
			if now.Sub(e.CreatedAt) <= s.ttl {
				live = append(live, e)
			}
		}
		entries = live
	}
	if s.maxPerUser > 0 && len(entries) > s.maxPerUser {
		entries = entries[len(entries)-s.maxPerUser:]
	}
	return entries
}

// startPeriodicCompaction runs compactNow on a ticker until Close is called.
func (s *MemoryStore) startPeriodicCompaction(interval time.Duration) {
	s.stopCompact = make(chan struct{})
	s.compactDone = make(chan struct{})
	go func() {
		defer close(s.compactDone)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				s.compactNow()
			case <-s.stopCompact:
				return
			}
		}
	}()
}

// compactNow prunes every user's entries and rewrites the journal to match,
// so a long-running process doesn't grow the file forever even though
// in-memory usage already stays bounded via pruneLocked on every read/write.
//
// Holds s.mu for the whole operation, including the disk write — a Save that
// arrives mid-compaction must either land before the snapshot (and so is
// included in it) or block until compaction finishes (and so writes its own
// journal line after the file has been reopened). Given the caps already in
// place, the snapshot+write is small and fast; without holding s.mu
// throughout, a Save landing between the snapshot and the file swap could
// have its journal line silently dropped by the rewrite.
func (s *MemoryStore) compactNow() {
	s.mu.Lock()
	defer s.mu.Unlock()

	var live []Entry
	for userKey, entries := range s.entries {
		pruned := s.pruneLocked(entries)
		if len(pruned) == 0 {
			delete(s.entries, userKey)
			continue
		}
		s.entries[userKey] = pruned
		live = append(live, pruned...)
	}

	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if s.file != nil {
		s.file.Close()
		s.file = nil
	}
	s.compactJournal(s.filePath, live)
	if af, err := os.OpenFile(s.filePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600); err == nil {
		s.file = af
	} else {
		slog.Warn("memory: journal unavailable after compaction, running in-memory only", "err", err)
	}
}

// Close stops background compaction (if running) and flushes/closes the
// journal file.
func (s *MemoryStore) Close() error {
	if s.stopCompact != nil {
		close(s.stopCompact)
		<-s.compactDone
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	if s.file != nil {
		return s.file.Close()
	}
	return nil
}

func (s *MemoryStore) replayJournal(path string) {
	f, err := os.Open(path)
	if err != nil {
		// First run — open for writing.
		af, werr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
		if werr == nil {
			s.file = af
		}
		return
	}

	var total, loaded int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	for scanner.Scan() {
		total++
		var e Entry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		if s.ttl > 0 && time.Since(e.CreatedAt) > s.ttl {
			continue // expired — compaction below will drop it from disk too
		}
		s.entries[e.UserKey] = append(s.entries[e.UserKey], e)
		loaded++
	}
	f.Close()

	// Apply the per-user cap now that every line has been read, so
	// compaction below writes back only what Save would have kept anyway.
	var live []Entry
	for userKey, entries := range s.entries {
		s.entries[userKey] = s.pruneLocked(entries)
		live = append(live, s.entries[userKey]...)
	}
	if loaded > 0 {
		slog.Info("memory: replayed journal", "entries", len(live), "path", path)
	}

	if len(live) < total {
		s.compactJournal(path, live)
	}

	af, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Warn("memory: journal unavailable, running in-memory only", "err", err)
		return
	}
	s.file = af
}

// compactJournal rewrites path with only live entries. Writes to a temp file
// first and renames atomically — a crash mid-write leaves the original intact.
func (s *MemoryStore) compactJournal(path string, entries []Entry) {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0600)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	for _, e := range entries {
		line, err := json.Marshal(e)
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
	slog.Info("memory: compacted journal", "kept", len(entries), "path", path)
}

func (s *MemoryStore) appendJournal(e Entry) {
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	s.fileMu.Lock()
	defer s.fileMu.Unlock()
	// s.file can be nil (no persistence configured) or swapped out from under
	// us mid-write by compactNow's close/reopen — the nil check must happen
	// under the same lock compactNow uses to mutate it, not before acquiring it.
	if s.file == nil {
		return
	}
	_, _ = s.file.Write(append(line, '\n'))
}
