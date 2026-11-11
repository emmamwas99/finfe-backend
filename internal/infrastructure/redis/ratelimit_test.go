package redis

import (
	"context"
	"testing"
	"time"
)

func TestMemoryLimiter(t *testing.T) {
	l := NewMemoryLimiter()
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	for i := 0; i < 3; i++ {
		if ok, _ := l.Allow(context.Background(), "k", 3, time.Minute); !ok {
			t.Fatalf("hit %d should pass", i)
		}
	}
	if ok, _ := l.Allow(context.Background(), "k", 3, time.Minute); ok {
		t.Fatal("4th hit should be limited")
	}
	if ok, _ := l.Allow(context.Background(), "other", 3, time.Minute); !ok {
		t.Fatal("keys are independent")
	}
	now = now.Add(61 * time.Second)
	if ok, _ := l.Allow(context.Background(), "k", 3, time.Minute); !ok {
		t.Fatal("new window resets")
	}
}
