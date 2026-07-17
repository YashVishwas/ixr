package memory

import (
	"context"
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
