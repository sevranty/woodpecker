// Copyright 2026 Woodpecker Authors
// Licensed under the Apache License, Version 2.0 (the "License").
package github

import (
	"context"
	"crypto/sha256"
	"sync"
	"time"
)

const (
	defaultBranchCacheTTL = 15 * time.Second
	maxBranchCacheEntries = 256
)

type branchCacheKey struct {
	credential [32]byte
	owner      string
	repo       string
	page       int
	perPage    int
}

func newBranchCacheKey(token, owner, repo string, page, perPage int) branchCacheKey {
	return branchCacheKey{
		credential: sha256.Sum256([]byte(token)),
		owner:      owner, repo: repo, page: page, perPage: perPage,
	}
}

type branchCacheEntry struct {
	value   []string
	err     error
	expires time.Time
}

type branchFlight struct {
	done  chan struct{}
	value []string
	err   error
}

type branchCache struct {
	mu       sync.Mutex
	entries  map[branchCacheKey]branchCacheEntry
	inFlight map[branchCacheKey]*branchFlight
	ttl      time.Duration // test override; zero means defaultBranchCacheTTL
}

func copyBranches(in []string) []string {
	if in == nil {
		return nil
	}
	return append([]string{}, in...)
}

// get returns a short-lived, token-scoped snapshot of successful branch lists.
// Concurrent requests share a single HTTP call. Only explicit rate-limit errors
// receive a temporary cooldown; ordinary permission errors are never cached.
func (c *branchCache) get(
	ctx context.Context,
	key branchCacheKey,
	fetch func() (names []string, err error, cooldown time.Duration),
) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	c.mu.Lock()
	if entry, ok := c.entries[key]; ok && time.Now().Before(entry.expires) {
		c.mu.Unlock()
		return copyBranches(entry.value), entry.err
	}
	if flight, ok := c.inFlight[key]; ok {
		c.mu.Unlock()
		select {
		case <-flight.done:
			return copyBranches(flight.value), flight.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if c.inFlight == nil {
		c.inFlight = make(map[branchCacheKey]*branchFlight)
	}
	flight := &branchFlight{done: make(chan struct{})}
	c.inFlight[key] = flight
	c.mu.Unlock()

	// The initiator makes the request without holding the cache mutex.
	names, err, cooldown := fetch()

	c.mu.Lock()
	flight.value, flight.err = copyBranches(names), err
	expiry := time.Duration(0)
	if err == nil {
		expiry = c.ttl
		if expiry == 0 {
			expiry = defaultBranchCacheTTL
		}
	} else if cooldown > 0 {
		expiry = cooldown
	}
	if expiry > 0 {
		if c.entries == nil {
			c.entries = make(map[branchCacheKey]branchCacheEntry)
		}
		now := time.Now()
		// Never allow a long-lived unbounded cache on a busy multi-user forge.
		for k, entry := range c.entries {
			if !now.Before(entry.expires) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= maxBranchCacheEntries {
			var oldestKey branchCacheKey
			var oldest time.Time
			first := true
			for k, entry := range c.entries {
				if first || entry.expires.Before(oldest) {
					first, oldestKey, oldest = false, k, entry.expires
				}
			}
			delete(c.entries, oldestKey)
		}
		c.entries[key] = branchCacheEntry{value: copyBranches(names), err: err, expires: now.Add(expiry)}
	}
	delete(c.inFlight, key)
	close(flight.done)
	c.mu.Unlock()
	return copyBranches(names), err
}
