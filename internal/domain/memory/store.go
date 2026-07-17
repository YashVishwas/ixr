// Package memory provides persistent user-level fact storage for ixr.
// Facts are extracted from conversation turns and stored keyed by userKey
// (tenantID:userID). On new sessions, relevant memories are retrieved and
// injected as context so the model remembers the user across conversations.
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
	UserKey   string    `json:"user_key"`   // tenantID:userID
	Category  string    `json:"category"`   // name | employer | project | location | role | preference | other
	Content   string    `json:"content"`    // "User's name is Arun"
	Source    string    `json:"source"`     // "rule" | "llm"
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

// MemoryStore is an in-process store backed by an optional append-only journal.
// Safe for concurrent use.
type MemoryStore struct {
	mu      sync.Mutex
	entries map[string][]Entry // userKey → ordered list (oldest first)
	file    *os.File
	fileMu  sync.Mutex
}

// NewMemoryStore creates a store. dir="" means in-memory only.
func NewMemoryStore(dir string) *MemoryStore {
	s := &MemoryStore{entries: make(map[string][]Entry)}
	if dir != "" {
		s.replayJournal(filepath.Join(dir, "memory.jsonl"))
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
	s.entries[e.UserKey] = append(s.entries[e.UserKey], e)
	s.mu.Unlock()

	s.appendJournal(e)
	return nil
}

func (s *MemoryStore) Recent(_ context.Context, userKey string, n int) ([]Entry, error) {
	s.mu.Lock()
	all := make([]Entry, len(s.entries[userKey]))
	copy(all, s.entries[userKey])
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
	cp := make([]Entry, len(s.entries[userKey]))
	copy(cp, s.entries[userKey])
	return cp, nil
}

// Close flushes and closes the journal file.
func (s *MemoryStore) Close() error {
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
	defer f.Close()

	var loaded int
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4<<20), 4<<20)
	for scanner.Scan() {
		var e Entry
		if json.Unmarshal(scanner.Bytes(), &e) != nil {
			continue
		}
		s.entries[e.UserKey] = append(s.entries[e.UserKey], e)
		loaded++
	}
	if loaded > 0 {
		slog.Info("memory: replayed journal", "entries", loaded, "path", path)
	}

	af, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if err != nil {
		slog.Warn("memory: journal unavailable, running in-memory only", "err", err)
		return
	}
	s.file = af
}

func (s *MemoryStore) appendJournal(e Entry) {
	if s.file == nil {
		return
	}
	line, err := json.Marshal(e)
	if err != nil {
		return
	}
	s.fileMu.Lock()
	_, _ = s.file.Write(append(line, '\n'))
	s.fileMu.Unlock()
}
