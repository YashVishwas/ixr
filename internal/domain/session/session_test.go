package session

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YashVishwas/ixr/pkg/schema"
)

func makeMsg(role, content string) schema.Message {
	return schema.Message{Role: role, Content: content}
}

// --- MemorySessionStore ---

func TestMemory_GetMiss(t *testing.T) {
	s := NewMemorySessionStore(time.Minute, 10)
	defer s.Close()
	_, ok := s.Get(context.Background(), "k")
	if ok {
		t.Fatal("expected miss on empty store")
	}
}

func TestMemory_AppendAndGet(t *testing.T) {
	s := NewMemorySessionStore(time.Minute, 10)
	defer s.Close()
	ctx := context.Background()

	s.Append(ctx, "k", makeMsg("user", "hello"), makeMsg("assistant", "hi"))
	msgs, ok := s.Get(ctx, "k")
	if !ok {
		t.Fatal("expected hit after Append")
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Content != "hello" || msgs[1].Content != "hi" {
		t.Fatalf("unexpected messages: %+v", msgs)
	}
}

func TestMemory_MultipleTurns(t *testing.T) {
	s := NewMemorySessionStore(time.Minute, 10)
	defer s.Close()
	ctx := context.Background()

	s.Append(ctx, "k", makeMsg("user", "turn1"), makeMsg("assistant", "resp1"))
	s.Append(ctx, "k", makeMsg("user", "turn2"), makeMsg("assistant", "resp2"))

	msgs, ok := s.Get(ctx, "k")
	if !ok {
		t.Fatal("expected hit")
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(msgs))
	}
}

func TestMemory_MaxTurnsTrim(t *testing.T) {
	s := NewMemorySessionStore(time.Minute, 2) // max 2 turns = 4 messages
	defer s.Close()
	ctx := context.Background()

	for i := range 5 {
		s.Append(ctx, "k",
			makeMsg("user", string(rune('a'+i))),
			makeMsg("assistant", string(rune('A'+i))),
		)
	}
	msgs, _ := s.Get(ctx, "k")
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages (2 turns), got %d", len(msgs))
	}
	// Should keep the last 2 turns (d,D,e,E).
	if msgs[0].Content != "d" {
		t.Fatalf("expected oldest kept turn to be 'd', got %q", msgs[0].Content)
	}
}

func TestMemory_TTLExpiry(t *testing.T) {
	s := NewMemorySessionStore(time.Millisecond, 10)
	defer s.Close()
	ctx := context.Background()

	s.Append(ctx, "k", makeMsg("user", "q"), makeMsg("assistant", "a"))
	time.Sleep(5 * time.Millisecond)

	_, ok := s.Get(ctx, "k")
	if ok {
		t.Fatal("expected miss after TTL expiry")
	}
}

func TestMemory_Delete(t *testing.T) {
	s := NewMemorySessionStore(time.Minute, 10)
	defer s.Close()
	ctx := context.Background()

	s.Append(ctx, "k", makeMsg("user", "q"), makeMsg("assistant", "a"))
	s.Delete(ctx, "k")

	_, ok := s.Get(ctx, "k")
	if ok {
		t.Fatal("expected miss after Delete")
	}
}

func TestMemory_GetReturnsCopy(t *testing.T) {
	s := NewMemorySessionStore(time.Minute, 10)
	defer s.Close()
	ctx := context.Background()

	s.Append(ctx, "k", makeMsg("user", "q"), makeMsg("assistant", "a"))
	msgs, _ := s.Get(ctx, "k")
	msgs[0].Content = "mutated"

	msgs2, _ := s.Get(ctx, "k")
	if msgs2[0].Content == "mutated" {
		t.Fatal("Get should return a copy, not a reference to internal state")
	}
}

// --- PersistentSessionStore ---

func TestPersistent_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	ps := NewPersistentSessionStore(time.Minute, 10, dir)
	ps.Append(ctx, "k", makeMsg("user", "q1"), makeMsg("assistant", "a1"))
	ps.Append(ctx, "k", makeMsg("user", "q2"), makeMsg("assistant", "a2"))
	ps.Close()

	// Reload from journal.
	ps2 := NewPersistentSessionStore(time.Minute, 10, dir)
	defer ps2.Close()
	msgs, ok := ps2.Get(ctx, "k")
	if !ok {
		t.Fatal("expected hit after journal replay")
	}
	if len(msgs) != 4 {
		t.Fatalf("expected 4 messages after replay, got %d", len(msgs))
	}
	if msgs[0].Content != "q1" || msgs[3].Content != "a2" {
		t.Fatalf("unexpected messages after replay: %+v", msgs)
	}
}

func TestPersistent_ExpiredSessionPruned(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	ps := NewPersistentSessionStore(time.Millisecond, 10, dir)
	ps.Append(ctx, "k", makeMsg("user", "q"), makeMsg("assistant", "a"))
	ps.Close()

	time.Sleep(5 * time.Millisecond)

	ps2 := NewPersistentSessionStore(time.Minute, 10, dir)
	defer ps2.Close()
	_, ok := ps2.Get(ctx, "k")
	if ok {
		t.Fatal("expired session should not be loaded on replay")
	}
}

func TestPersistent_Compaction(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	ctx := context.Background()

	// Write several turns for two sessions.
	ps := NewPersistentSessionStore(time.Minute, 10, dir)
	ps.Append(ctx, "s1", makeMsg("user", "a"), makeMsg("assistant", "b"))
	ps.Append(ctx, "s1", makeMsg("user", "c"), makeMsg("assistant", "d"))
	ps.Append(ctx, "s2", makeMsg("user", "x"), makeMsg("assistant", "y"))
	ps.Close()

	// Journal should have 3 delta lines.
	info, _ := os.Stat(path)
	sizeBefore := info.Size()

	// Reload triggers compaction (3 lines → 2 compacted lines).
	ps2 := NewPersistentSessionStore(time.Minute, 10, dir)
	ps2.Close()

	info2, _ := os.Stat(path)
	if info2.Size() >= sizeBefore {
		t.Fatalf("expected journal to shrink after compaction: before=%d after=%d", sizeBefore, info2.Size())
	}
}

func TestPersistent_NoPersistence(t *testing.T) {
	// dir="" should not panic and work as pure in-memory.
	ps := NewPersistentSessionStore(time.Minute, 10, "")
	ctx := context.Background()
	ps.Append(ctx, "k", makeMsg("user", "q"), makeMsg("assistant", "a"))
	msgs, ok := ps.Get(ctx, "k")
	if !ok || len(msgs) != 2 {
		t.Fatalf("expected in-memory operation to work with dir=\"\": ok=%v len=%d", ok, len(msgs))
	}
	ps.Close()
}
