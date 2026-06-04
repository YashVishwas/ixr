package policy

import (
	"context"
	"sync"
	"testing"
	"time"

	cfgpkg "github.com/YashVishwas/ixr/internal/adapters/config"
	"github.com/YashVishwas/ixr/pkg/schema"
)

func makeID(tenant, user, usecase string) schema.Identity {
	return schema.Identity{TenantID: tenant, UserID: user, UseCaseID: usecase}
}

func TestMemoryRateLimiter_Unlimited(t *testing.T) {
	rl := NewMemoryRateLimiter(0, 0, time.Second)
	for i := 0; i < 100; i++ {
		d := rl.Check(context.Background(), makeID("t", "u", "uc"), 0)
		if !d.Allowed {
			t.Fatalf("unlimited limiter should always allow; denied on iteration %d", i)
		}
	}
}

func TestMemoryRateLimiter_RequestCap(t *testing.T) {
	rl := NewMemoryRateLimiter(3, 0, time.Minute)
	id1 := makeID("t1", "u1", "uc1")

	for i := 0; i < 3; i++ {
		d := rl.Check(context.Background(), id1, 0)
		if !d.Allowed {
			t.Fatalf("request %d should be allowed", i)
		}
		rl.Record(context.Background(), id1, 0)
	}

	d := rl.Check(context.Background(), id1, 0)
	if d.Allowed {
		t.Fatal("4th request should be denied after cap of 3")
	}
	if d.RetryAfter <= 0 {
		t.Errorf("RetryAfter should be positive, got %v", d.RetryAfter)
	}
}

func TestMemoryRateLimiter_TokenCap(t *testing.T) {
	rl := NewMemoryRateLimiter(0, 100, time.Minute)
	id1 := makeID("t2", "u2", "uc2")

	rl.Record(context.Background(), id1, 80)

	if d := rl.Check(context.Background(), id1, 30); d.Allowed {
		t.Fatal("request requiring 30 tokens should be denied when 80 already used (cap=100)")
	}
	if d := rl.Check(context.Background(), id1, 10); !d.Allowed {
		t.Fatal("request requiring 10 tokens should be allowed when 80 used (cap=100)")
	}
}

func TestMemoryRateLimiter_SeparateIdentities(t *testing.T) {
	rl := NewMemoryRateLimiter(2, 0, time.Minute)
	idA := makeID("tA", "uA", "uc")
	idB := makeID("tB", "uB", "uc")

	for i := 0; i < 2; i++ {
		rl.Record(context.Background(), idA, 0)
	}

	if d := rl.Check(context.Background(), idA, 0); d.Allowed {
		t.Fatal("A should be rate-limited")
	}
	if d := rl.Check(context.Background(), idB, 0); !d.Allowed {
		t.Fatal("B should not be rate-limited")
	}
}

func TestMemoryRateLimiter_ConcurrentSafety(t *testing.T) {
	rl := NewMemoryRateLimiter(1000, 0, time.Minute)
	id1 := makeID("tc", "uc", "ucc")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rl.Check(context.Background(), id1, 0)
			rl.Record(context.Background(), id1, 1)
		}()
	}
	wg.Wait()
}

func TestMemoryRateLimiter_Reload(t *testing.T) {
	rl := NewMemoryRateLimiter(1, 0, time.Minute)
	id1 := makeID("tr", "ur", "ucr")
	rl.Record(context.Background(), id1, 0)

	if d := rl.Check(context.Background(), id1, 0); d.Allowed {
		t.Fatal("should be limited after 1 record with cap=1")
	}

	rl.Reload(cfgpkg.RateLimitConfig{MaxRequests: 10, WindowSec: 60})
	if d := rl.Check(context.Background(), id1, 0); !d.Allowed {
		t.Fatal("should be allowed after reload with higher cap")
	}
}
