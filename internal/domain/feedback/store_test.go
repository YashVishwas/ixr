package feedback

import (
	"sync"
	"testing"
	"time"
)

func TestStore_RecordThenLookup_RoundTrips(t *testing.T) {
	s := NewStore(0, time.Minute)
	s.Record("chatcmpl-1", CallInfo{Model: "gpt-4o", AutoRouted: true})

	got, ok := s.Lookup("chatcmpl-1")
	if !ok {
		t.Fatal("expected a hit")
	}
	if got.Model != "gpt-4o" || !got.AutoRouted {
		t.Errorf("got %+v, want {Model: gpt-4o, AutoRouted: true}", got)
	}
}

func TestStore_Lookup_UnknownID_Miss(t *testing.T) {
	s := NewStore(0, time.Minute)
	if _, ok := s.Lookup("does-not-exist"); ok {
		t.Fatal("expected a miss for an unknown ID")
	}
}

func TestStore_Record_EmptyCallID_NotIndexed(t *testing.T) {
	s := NewStore(0, time.Minute)
	s.Record("", CallInfo{Model: "gpt-4o"})
	if s.Len() != 0 {
		t.Fatalf("expected an empty call ID not to be indexed, got Len()=%d", s.Len())
	}
}

func TestStore_TTLExpiry(t *testing.T) {
	s := NewStore(0, time.Millisecond)
	s.Record("chatcmpl-1", CallInfo{Model: "gpt-4o"})
	time.Sleep(5 * time.Millisecond)
	if _, ok := s.Lookup("chatcmpl-1"); ok {
		t.Fatal("expected a miss after TTL expiry")
	}
}

func TestStore_ZeroTTL_NeverExpires(t *testing.T) {
	s := NewStore(0, 0)
	s.Record("chatcmpl-1", CallInfo{Model: "gpt-4o"})
	time.Sleep(2 * time.Millisecond)
	if _, ok := s.Lookup("chatcmpl-1"); !ok {
		t.Fatal("expected a hit — ttl<=0 means no expiry")
	}
}

func TestStore_MaxSizeEvictsToStayWithinBound(t *testing.T) {
	s := NewStore(2, time.Minute)
	s.Record("id1", CallInfo{Model: "a"})
	s.Record("id2", CallInfo{Model: "b"})
	s.Record("id3", CallInfo{Model: "c"})

	if s.Len() > 2 {
		t.Fatalf("store exceeded maxSize: len=%d", s.Len())
	}
}

func TestStore_ConcurrentRecordLookup_NoRace(t *testing.T) {
	s := NewStore(1000, time.Minute)
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			id := "chatcmpl-" + string(rune('a'+i%26))
			s.Record(id, CallInfo{Model: "gpt-4o"})
			s.Lookup(id)
		}(i)
	}
	wg.Wait()
}
