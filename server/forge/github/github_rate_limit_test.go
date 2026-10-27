package github

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestBranchCacheReusesCopyAndExpires(t *testing.T) {
	c := &client{}
	var calls int
	key := [32]byte{1}
	fetch := func() ([]string, error) { calls++; return []string{"main", "release"}, nil }
	a, err := c.cachedBranches(context.Background(), key, fetch)
	if err != nil {
		t.Fatal(err)
	}
	a[0] = "tampered"
	b, err := c.cachedBranches(context.Background(), key, fetch)
	if err != nil || b[0] != "main" || calls != 1 {
		t.Fatalf("cache isolation: %v %v %d", b, err, calls)
	}
	c.branchMu.Lock()
	c.branchCache[key].expires = time.Now().Add(-time.Second)
	c.branchMu.Unlock()
	_, err = c.cachedBranches(context.Background(), key, fetch)
	if err != nil || calls != 2 {
		t.Fatalf("expiry fetch: %v count=%d", err, calls)
	}
}

func TestBranchCacheCoalescesConcurrent(t *testing.T) {
	c := &client{}
	key := [32]byte{2}
	var calls atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	fetch := func() ([]string, error) { calls.Add(1); close(started); <-release; return []string{"main"}, nil }
	var wg sync.WaitGroup
	wg.Add(2)
	results := make([][]string, 2)
	errs := make([]error, 2)
	go func() { defer wg.Done(); results[0], errs[0] = c.cachedBranches(context.Background(), key, fetch) }()
	<-started
	go func() { defer wg.Done(); results[1], errs[1] = c.cachedBranches(context.Background(), key, fetch) }()
	close(release)
	wg.Wait()
	if calls.Load() != 1 || errs[0] != nil || errs[1] != nil || len(results[0]) != 1 || len(results[1]) != 1 {
		t.Fatalf("single fetch: calls=%d errs=%v", calls.Load(), errs)
	}
}

func TestBranchCacheDoesNotCacheErrors(t *testing.T) {
	c := &client{}
	key := [32]byte{3}
	var calls int
	sentinel := errors.New("github 403")
	fetch := func() ([]string, error) {
		calls++
		if calls == 1 {
			return nil, sentinel
		}
		return []string{"ok"}, nil
	}
	_, err := c.cachedBranches(context.Background(), key, fetch)
	if !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	b, err := c.cachedBranches(context.Background(), key, fetch)
	if err != nil || len(b) != 1 || calls != 2 {
		t.Fatalf("retry after error: %v %v %d", b, err, calls)
	}
}

func TestBranchCacheDifferentKeysAndCancelledWaiter(t *testing.T) {
	c := &client{}
	started := make(chan struct{})
	release := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = c.cachedBranches(context.Background(), [32]byte{4}, func() ([]string, error) { close(started); <-release; return []string{"ok"}, nil })
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.cachedBranches(ctx, [32]byte{4}, func() ([]string, error) { t.Error("duplicate fetch"); return nil, nil })
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	_, err = c.cachedBranches(context.Background(), [32]byte{5}, func() ([]string, error) { return []string{"other"}, nil })
	if err != nil {
		t.Fatal(err)
	}
	close(release)
	wg.Wait()
}
