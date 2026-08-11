package memory

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- Store ---

func TestStore_SaveAndRecent(t *testing.T) {
	s := NewMemoryStore("")
	ctx := context.Background()

	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "User's name is Arun", Source: "rule"})
	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "project", Content: "User is building ixr", Source: "rule"})

	entries, err := s.Recent(ctx, "acme", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestStore_RecentDeduplicatesByCategory(t *testing.T) {
	s := NewMemoryStore("")
	ctx := context.Background()

	// Two name entries — most recent should win in deduplication.
	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "User's name is Bob", Source: "rule", CreatedAt: time.Now().Add(-time.Hour)})
	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "User's name is Arun", Source: "rule", CreatedAt: time.Now()})

	entries, err := s.Recent(ctx, "acme", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated name entry, got %d", len(entries))
	}
	if entries[0].Content != "User's name is Arun" {
		t.Fatalf("expected most recent name, got %q", entries[0].Content)
	}
}

func TestStore_RecentRespectTopK(t *testing.T) {
	s := NewMemoryStore("")
	ctx := context.Background()

	for i, cat := range []string{"name", "employer", "project", "location", "role", "preference"} {
		_ = s.Save(ctx, Entry{
			UserKey:   "acme",
			Category:  cat,
			Content:   "fact " + cat,
			Source:    "rule",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	entries, _ := s.Recent(ctx, "acme", 3)
	if len(entries) != 3 {
		t.Fatalf("expected 3 entries with topK=3, got %d", len(entries))
	}
}

func TestStore_IsolatedByUserKey(t *testing.T) {
	s := NewMemoryStore("")
	ctx := context.Background()

	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "User's name is Arun", Source: "rule"})
	_ = s.Save(ctx, Entry{UserKey: "other", Category: "name", Content: "User's name is Bob", Source: "rule"})

	entries, _ := s.Recent(ctx, "acme", 10)
	if len(entries) != 1 || entries[0].Content != "User's name is Arun" {
		t.Fatalf("acme memories should not include other's entries: %+v", entries)
	}
}

func TestStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1 := NewMemoryStore(dir)
	_ = s1.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "User's name is Arun", Source: "rule"})
	s1.Close()

	// Reload from journal.
	s2 := NewMemoryStore(dir)
	entries, _ := s2.Recent(ctx, "acme", 10)
	if len(entries) != 1 || entries[0].Content != "User's name is Arun" {
		t.Fatalf("expected replayed entry, got: %+v", entries)
	}
	s2.Close()
}

// --- Delete ---

func TestStore_Delete_RemovesEntry(t *testing.T) {
	s := NewMemoryStore("")
	ctx := context.Background()

	_ = s.Save(ctx, Entry{ID: "e1", UserKey: "acme", Category: "name", Content: "User's name is Arun"})
	_ = s.Save(ctx, Entry{ID: "e2", UserKey: "acme", Category: "project", Content: "User is building ixr"})

	if err := s.Delete(ctx, "acme", "e1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	all, _ := s.All(ctx, "acme")
	if len(all) != 1 || all[0].ID != "e2" {
		t.Fatalf("expected only e2 to remain, got: %+v", all)
	}
}

func TestStore_Delete_UnknownID_ErrNotFound(t *testing.T) {
	s := NewMemoryStore("")
	ctx := context.Background()
	_ = s.Save(ctx, Entry{ID: "e1", UserKey: "acme", Category: "name", Content: "x"})

	err := s.Delete(ctx, "acme", "does-not-exist")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestStore_Delete_ScopedToUserKey_CannotDeleteAnotherUsersEntry(t *testing.T) {
	s := NewMemoryStore("")
	ctx := context.Background()
	_ = s.Save(ctx, Entry{ID: "e1", UserKey: "acme", Category: "name", Content: "Arun's fact"})

	err := s.Delete(ctx, "other-tenant:someone-else", "e1")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound when deleting under the wrong userKey, got: %v", err)
	}

	// Confirm it's genuinely untouched, not just an error with a side effect.
	all, _ := s.All(ctx, "acme")
	if len(all) != 1 {
		t.Fatalf("expected the entry to survive a delete attempt under the wrong userKey, got: %+v", all)
	}
}

func TestStore_Delete_SurvivesRestart(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1 := NewMemoryStore(dir)
	_ = s1.Save(ctx, Entry{ID: "e1", UserKey: "acme", Category: "name", Content: "keep"})
	_ = s1.Save(ctx, Entry{ID: "e2", UserKey: "acme", Category: "project", Content: "delete me"})
	if err := s1.Delete(ctx, "acme", "e2"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	s1.Close()

	// The journal is append-only — Delete must rewrite it, or a restart
	// would replay e2 right back into existence from the original Save line.
	s2 := NewMemoryStore(dir)
	all, _ := s2.All(ctx, "acme")
	if len(all) != 1 || all[0].ID != "e1" {
		t.Fatalf("expected only e1 to survive a restart after deleting e2, got: %+v", all)
	}
	s2.Close()
}

// --- Bounded growth: TTL + per-user cap ---
// Without these, both the in-process map and the on-disk journal grow
// forever as long as a user keeps talking — this is what "unlimited memory"
// meant in practice. Bounding it approximates "scoped to a session" without
// needing to actually key memory by session ID.

func TestStore_ExpiresEntriesOlderThanTTL(t *testing.T) {
	s := NewMemoryStoreWithLimits("", time.Hour, 0, 0)
	ctx := context.Background()

	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "stale", CreatedAt: time.Now().Add(-2 * time.Hour)})
	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "project", Content: "fresh", CreatedAt: time.Now()})

	entries, _ := s.Recent(ctx, "acme", 10)
	if len(entries) != 1 || entries[0].Content != "fresh" {
		t.Fatalf("expected only the non-expired entry, got: %+v", entries)
	}

	all, _ := s.All(ctx, "acme")
	if len(all) != 1 {
		t.Fatalf("expired entry should be pruned from storage, not just filtered on read: %+v", all)
	}
}

func TestStore_ZeroTTLNeverExpires(t *testing.T) {
	s := NewMemoryStoreWithLimits("", 0, 0, 0)
	ctx := context.Background()

	_ = s.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "ancient", CreatedAt: time.Now().Add(-24 * 365 * time.Hour)})

	entries, _ := s.Recent(ctx, "acme", 10)
	if len(entries) != 1 {
		t.Fatalf("ttl<=0 should mean entries never expire, got: %+v", entries)
	}
}

func TestStore_CapsEntriesPerUser(t *testing.T) {
	s := NewMemoryStoreWithLimits("", 0, 5, 0)
	ctx := context.Background()

	for i := 0; i < 20; i++ {
		_ = s.Save(ctx, Entry{
			UserKey:   "acme",
			Category:  "preference",
			Content:   "fact",
			CreatedAt: time.Now().Add(time.Duration(i) * time.Second),
		})
	}

	all, _ := s.All(ctx, "acme")
	if len(all) != 5 {
		t.Fatalf("expected storage capped at 5 entries regardless of how many were saved, got %d", len(all))
	}
}

func TestStore_ReplayCompactsExpiredAndOverCapEntries(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	s1 := NewMemoryStoreWithLimits(dir, time.Hour, 2, 0)
	_ = s1.Save(ctx, Entry{UserKey: "acme", Category: "name", Content: "stale", CreatedAt: time.Now().Add(-2 * time.Hour)})
	for i := 0; i < 5; i++ {
		_ = s1.Save(ctx, Entry{UserKey: "acme", Category: "preference", Content: "fact", CreatedAt: time.Now()})
	}
	s1.Close()

	// Reopening should replay only what's still live and cap-compliant, and
	// rewrite the journal to match — the file shouldn't keep growing forever
	// across restarts either.
	s2 := NewMemoryStoreWithLimits(dir, time.Hour, 2, 0)
	all, _ := s2.All(ctx, "acme")
	if len(all) != 2 {
		t.Fatalf("expected replay to cap at 2 live entries, got %d: %+v", len(all), all)
	}
	s2.Close()

	if lines := journalLineCount(t, filepath.Join(dir, "memory.jsonl")); lines != 2 {
		t.Fatalf("expected the journal to be compacted to 2 lines on disk, got %d", lines)
	}
}

func journalLineCount(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	trimmed := strings.TrimSpace(string(raw))
	if trimmed == "" {
		return 0
	}
	return strings.Count(trimmed, "\n") + 1
}

func TestStore_PeriodicCompactionShrinksJournalWithoutRestart(t *testing.T) {
	// The bug this closes: appendJournal writes every Save unconditionally,
	// so without periodic compaction the on-disk file grows forever across
	// the lifetime of a single long-running process, even though in-memory
	// usage already stays bounded by TTL/maxPerUser. Only a restart used to
	// shrink it (via replayJournal's one-time compaction).
	dir := t.TempDir()
	ctx := context.Background()
	path := filepath.Join(dir, "memory.jsonl")

	s := NewMemoryStoreWithLimits(dir, 0, 2, 0) // compaction driven manually below, not on a ticker
	defer s.Close()

	for i := 0; i < 10; i++ {
		_ = s.Save(ctx, Entry{UserKey: "acme", Category: "preference", Content: "fact", CreatedAt: time.Now()})
	}
	if got := journalLineCount(t, path); got != 10 {
		t.Fatalf("expected all 10 raw appends on disk before compaction, got %d", got)
	}

	s.compactNow()

	if got := journalLineCount(t, path); got != 2 {
		t.Fatalf("expected compactNow to shrink the journal to the 2-entry cap without a restart, got %d", got)
	}
	all, _ := s.All(ctx, "acme")
	if len(all) != 2 {
		t.Fatalf("in-memory state should still reflect the cap after compaction, got %d", len(all))
	}
}

func TestStore_ConcurrentSavesDuringCompactionAreNotLost(t *testing.T) {
	// compactNow holds s.mu for its whole duration specifically so a Save
	// racing with it can't have its journal line silently dropped by the
	// rewrite — this exercises that under -race with saves and compactions
	// interleaved from multiple goroutines.
	dir := t.TempDir()
	ctx := context.Background()
	s := NewMemoryStoreWithLimits(dir, 0, 1000, 0)
	defer s.Close()

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = s.Save(ctx, Entry{UserKey: "acme", Category: "c" + strconv.Itoa(i), Content: "fact"})
		}(i)
	}
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s.compactNow()
		}()
	}
	wg.Wait()
	s.compactNow() // final pass so the journal reflects whatever s.entries settled on

	all, _ := s.All(ctx, "acme")
	inMemoryCount := len(all)
	onDiskCount := journalLineCount(t, filepath.Join(dir, "memory.jsonl"))
	if onDiskCount != inMemoryCount {
		t.Fatalf("journal (%d lines) should match in-memory state (%d entries) after compaction — a mismatch means a concurrent Save's line was dropped", onDiskCount, inMemoryCount)
	}
}

func TestStore_PeriodicCompactionRunsOnTicker(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()
	path := filepath.Join(dir, "memory.jsonl")

	s := NewMemoryStoreWithLimits(dir, 0, 2, 20*time.Millisecond)
	defer s.Close()

	for i := 0; i < 10; i++ {
		_ = s.Save(ctx, Entry{UserKey: "acme", Category: "preference", Content: "fact", CreatedAt: time.Now()})
	}

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if journalLineCount(t, path) == 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("expected background compaction to shrink the journal to 2 lines within 2s, got %d", journalLineCount(t, path))
}

func TestStore_CloseStopsBackgroundCompaction(t *testing.T) {
	dir := t.TempDir()
	s := NewMemoryStoreWithLimits(dir, 0, 0, 5*time.Millisecond)
	if err := s.Close(); err != nil {
		t.Fatalf("Close error: %v", err)
	}
	// Give a stray tick a chance to fire if the goroutine wasn't actually
	// stopped — a write to the now-closed file would be the observable bug.
	time.Sleep(50 * time.Millisecond)
}

// --- RuleExtractor ---

func TestRuleExtractor_Name(t *testing.T) {
	cases := []struct {
		msg      string
		wantCat  string
		wantFrag string
	}{
		{"My name is Arun.", "name", "Arun"},
		{"I'm called Alice.", "name", "Alice"},
		{"Call me Bob Smith.", "name", "Bob Smith"},
	}
	ext := RuleExtractor{}
	for _, tc := range cases {
		entries, _ := ext.Extract(context.Background(), "acme", ConversationTurn{UserMessage: tc.msg})
		found := false
		for _, e := range entries {
			if e.Category == tc.wantCat && containsFold(e.Content, tc.wantFrag) {
				found = true
			}
		}
		if !found {
			t.Errorf("msg=%q: expected category=%q containing %q, got %+v", tc.msg, tc.wantCat, tc.wantFrag, entries)
		}
	}
}

func TestRuleExtractor_NameDoesNotSwallowTrailingWord(t *testing.T) {
	// Regression: the name pattern's trigger phrase is meant to be
	// case-insensitive while the captured name stays case-sensitive
	// (["A-Z"] bounds it to an actual proper noun). A prior version applied
	// (?i) to the whole pattern, which made [A-Z] match lowercase too, so
	// "my name is Arun and I work at..." captured "Arun and" instead of "Arun".
	ext := RuleExtractor{}
	entries, _ := ext.Extract(context.Background(), "acme", ConversationTurn{
		UserMessage: "Hi, my name is Arun and I work at IXLabs.",
	})
	var name, employer string
	for _, e := range entries {
		switch e.Category {
		case "name":
			name = e.Content
		case "employer":
			employer = e.Content
		}
	}
	if name != "User's name is Arun" {
		t.Errorf("name: got %q, want %q", name, "User's name is Arun")
	}
	if employer != "User works at IXLabs" {
		t.Errorf("employer: got %q, want %q", employer, "User works at IXLabs")
	}
}

func TestRuleExtractor_Project(t *testing.T) {
	ext := RuleExtractor{}
	entries, _ := ext.Extract(context.Background(), "acme", ConversationTurn{
		UserMessage: "I'm building ixr, an LLM gateway.",
	})
	if len(entries) == 0 {
		t.Fatal("expected project extraction")
	}
	if entries[0].Category != "project" {
		t.Fatalf("expected category=project, got %q", entries[0].Category)
	}
}

func TestRuleExtractor_EmptyMessage(t *testing.T) {
	ext := RuleExtractor{}
	entries, err := ext.Extract(context.Background(), "acme", ConversationTurn{})
	if err != nil || len(entries) != 0 {
		t.Fatalf("empty message should return no entries: err=%v entries=%v", err, entries)
	}
}

func TestRuleExtractor_NoMatch(t *testing.T) {
	ext := RuleExtractor{}
	entries, _ := ext.Extract(context.Background(), "acme", ConversationTurn{
		UserMessage: "What is the capital of France?",
	})
	if len(entries) != 0 {
		t.Fatalf("generic question should produce no memories, got %+v", entries)
	}
}

func TestRuleExtractor_Source(t *testing.T) {
	ext := RuleExtractor{}
	entries, _ := ext.Extract(context.Background(), "acme", ConversationTurn{
		UserMessage: "My name is Arun.",
	})
	for _, e := range entries {
		if e.Source != "rule" {
			t.Fatalf("rule extractor should set Source=rule, got %q", e.Source)
		}
	}
}

// --- MultiExtractor ---

func TestMultiExtractor_DeduplicatesAcrossExtractors(t *testing.T) {
	// Two extractors both return the same category+content — should deduplicate.
	ext1 := &staticExtractor{[]Entry{{Category: "name", Content: "User's name is Arun"}}}
	ext2 := &staticExtractor{[]Entry{{Category: "name", Content: "User's name is Arun"}}}
	multi := NewMultiExtractor(ext1, ext2)
	entries, _ := multi.Extract(context.Background(), "acme", ConversationTurn{UserMessage: "x"})
	if len(entries) != 1 {
		t.Fatalf("expected 1 deduplicated entry, got %d", len(entries))
	}
}

type staticExtractor struct{ out []Entry }

func (s *staticExtractor) Extract(_ context.Context, userKey string, _ ConversationTurn) ([]Entry, error) {
	for i := range s.out {
		s.out[i].UserKey = userKey
	}
	return s.out, nil
}

func containsFold(s, sub string) bool {
	return len(s) >= len(sub) &&
		(s == sub || len(sub) == 0 ||
			func() bool {
				ls, lsub := len(s), len(sub)
				for i := 0; i <= ls-lsub; i++ {
					if equalFold(s[i:i+lsub], sub) {
						return true
					}
				}
				return false
			}())
}

func equalFold(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		ca, cb := a[i], b[i]
		if ca >= 'A' && ca <= 'Z' {
			ca += 'a' - 'A'
		}
		if cb >= 'A' && cb <= 'Z' {
			cb += 'a' - 'A'
		}
		if ca != cb {
			return false
		}
	}
	return true
}
