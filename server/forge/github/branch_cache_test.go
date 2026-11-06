package github

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBranchCacheReuseAndIdentity(t *testing.T) {
	var cache branchCache
	key := newBranchCacheKey("tokenA", "owner", "repo", 1, 50)
	var calls int
	fetch := func() ([]string, error, time.Duration) {
		calls++
		return []string{"main"}, nil, 0
	}
	a, err := cache.get(context.Background(), key, fetch)
	if err != nil || calls != 1 || len(a) != 1 {
		t.Fatalf("first call: result=%v err=%v calls=%d", a, err, calls)
	}
	a[0] = "modified"
	b, err := cache.get(context.Background(), key, fetch)
	if err != nil || calls != 1 || b[0] != "main" {
		t.Fatalf("cached result copied: result=%v err=%v calls=%d", b, err, calls)
	}
	_, _ = cache.get(context.Background(), newBranchCacheKey("tokenB", "owner", "repo", 1, 50), fetch)
	_, _ = cache.get(context.Background(), newBranchCacheKey("tokenA", "owner", "repo", 2, 50), fetch)
	_, _ = cache.get(context.Background(), newBranchCacheKey("tokenA", "other", "repo", 1, 50), fetch)
	if calls != 4 {
		t.Fatalf("different identity/page/repo were coalesced: calls=%d", calls)
	}
}

func TestBranchCacheConcurrentRequests(t *testing.T) {
	var cache branchCache
	key := newBranchCacheKey("token", "owner", "repo", 1, 50)
	var calls atomic.Int32
	var wait sync.WaitGroup
	start := make(chan struct{})
	for range 24 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			<-start
			got, err := cache.get(context.Background(), key, func() ([]string, error, time.Duration) {
				calls.Add(1)
				time.Sleep(10 * time.Millisecond)
				return []string{"main"}, nil, 0
			})
			if err != nil || len(got) != 1 || got[0] != "main" {
				t.Errorf("unexpected result %v, %v", got, err)
			}
		}()
	}
	close(start)
	wait.Wait()
	if calls.Load() != 1 {
		t.Fatalf("expected one API fetch, got %d", calls.Load())
	}
}

func TestBranchCacheExpiryAndErrors(t *testing.T) {
	var cache branchCache
	cache.ttl = 5 * time.Millisecond
	key := newBranchCacheKey("token", "o", "r", 1, 50)
	calls := 0
	fetch := func() ([]string, error, time.Duration) {
		calls++
		return []string{"main"}, nil, 0
	}
	_, _ = cache.get(context.Background(), key, fetch)
	time.Sleep(7 * time.Millisecond)
	_, _ = cache.get(context.Background(), key, fetch)
	if calls != 2 {
		t.Fatalf("expired cache reused, calls=%d", calls)
	}
	badKey := newBranchCacheKey("token", "o", "bad", 1, 50)
	permission := errors.New("permission denied")
	fail := func() ([]string, error, time.Duration) { calls++; return nil, permission, 0 }
	_, _ = cache.get(context.Background(), badKey, fail)
	_, _ = cache.get(context.Background(), badKey, fail)
	if calls != 4 {
		t.Fatalf("permission error was cached, calls=%d", calls)
	}
}

func TestBranchCacheRateLimitCooldown(t *testing.T) {
	var cache branchCache
	key := newBranchCacheKey("token", "o", "r", 1, 50)
	rateLimited := errors.New("rate limited")
	calls := 0
	fetch := func() ([]string, error, time.Duration) {
		calls++
		return nil, rateLimited, 100 * time.Millisecond
	}
	_, err := cache.get(context.Background(), key, fetch)
	if !errors.Is(err, rateLimited) {
		t.Fatalf("unexpected first error %v", err)
	}
	_, err = cache.get(context.Background(), key, fetch)
	if !errors.Is(err, rateLimited) || calls != 1 {
		t.Fatalf("cooldown ignored err=%v calls=%d", err, calls)
	}
	time.Sleep(105 * time.Millisecond)
	_, _ = cache.get(context.Background(), key, fetch)
	if calls != 2 {
		t.Fatalf("expired cooldown ignored: calls=%d", calls)
	}
}

func TestBranchCacheBounded(t *testing.T) {
	var cache branchCache
	for i := range maxBranchCacheEntries + 16 {
		key := newBranchCacheKey("token", "o", string(rune(i)), 1, 50)
		_, _ = cache.get(context.Background(), key, func() ([]string, error, time.Duration) {
			return []string{"main"}, nil, 0
		})
	}
	if len(cache.entries) > maxBranchCacheEntries {
		t.Fatalf("cache unbounded: %d entries", len(cache.entries))
	}
}

func TestBranchCacheCancelledContext(t *testing.T) {
	var cache branchCache
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	called := false
	_, err := cache.get(ctx, newBranchCacheKey("token", "o", "r", 1, 50), func() ([]string, error, time.Duration) {
		called = true
		return nil, nil, 0
	})
	if !errors.Is(err, context.Canceled) || called {
		t.Fatalf("cancelled fetch ran; err=%v called=%v", err, called)
	}
}
