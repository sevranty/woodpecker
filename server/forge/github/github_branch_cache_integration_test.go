// Copyright 2026 Woodpecker Authors
// Licensed under the Apache License, Version 2.0 (the "License").
package github

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/go-github/v92/github"
	"go.woodpecker-ci.org/woodpecker/v3/server/model"
)

func testGithubBranchesClient(t *testing.T, handler http.HandlerFunc) (context.Context, *client, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests.Add(1)
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	gh, err := github.NewClient(github.WithURLs(new(server.URL+"/"), nil), github.WithHTTPClient(server.Client()))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.WithValue(context.Background(), githubClientKey, gh)
	return ctx, &client{}, &requests
}

func TestGitHubBranchesDeduplicatesWithinTokenAndPage(t *testing.T) {
	ctx, forge, requests := testGithubBranchesClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/octocat/repo/branches" {
			t.Errorf("unexpected endpoint: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, `[{"name":"main"}]`)
	})
	user := &model.User{AccessToken: "token-1"}
	repo := &model.Repo{Owner: "octocat", Name: "repo"}
	opts := &model.ListOptions{Page: 1, PerPage: 50}
	for range 2 {
		list, err := forge.Branches(ctx, user, repo, opts)
		if err != nil || len(list) != 1 || list[0] != "main" {
			t.Fatalf("unexpected branches %v, error %v", list, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("expected one GitHub request, got %d", requests.Load())
	}
	_, _ = forge.Branches(ctx, user, repo, &model.ListOptions{Page: 2, PerPage: 50})
	_, _ = forge.Branches(ctx, &model.User{AccessToken: "token-2"}, repo, opts)
	if requests.Load() != 3 {
		t.Fatalf("different page/token requests incorrectly shared: %d", requests.Load())
	}
}

func TestGitHubBranchesRateLimitCooldown(t *testing.T) {
	ctx, forge, requests := testGithubBranchesClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Remaining", "0")
		w.Header().Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(40*time.Second).Unix(), 10))
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"message":"API rate limit exceeded"}`)
	})
	user := &model.User{AccessToken: "token"}
	repo := &model.Repo{Owner: "octocat", Name: "repo"}
	opts := &model.ListOptions{Page: 1, PerPage: 50}
	for range 2 {
		result, err := forge.Branches(ctx, user, repo, opts)
		if err == nil || result != nil {
			t.Fatalf("rate limit should be propagated, got %v / %v", result, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("rate-limit cooldown did not prevent second HTTP request: %d", requests.Load())
	}
}

func TestGitHubBranchesPermission403IsNotRateLimit(t *testing.T) {
	ctx, forge, requests := testGithubBranchesClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"message":"Resource not accessible by integration"}`)
	})
	for range 2 {
		if _, err := forge.Branches(ctx, &model.User{AccessToken: "token"}, &model.Repo{Owner: "octocat", Name: "repo"}, &model.ListOptions{Page: 1, PerPage: 50}); err == nil {
			t.Fatal("expected 403")
		}
	}
	if requests.Load() != 2 {
		t.Fatalf("ordinary 403 was cached as quota failure: %d", requests.Load())
	}
}

func TestGitHubBranchesSecondaryRetryAfter(t *testing.T) {
	ctx, forge, requests := testGithubBranchesClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Retry-After", "30")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = fmt.Fprint(w, `{"message":"secondary rate limit"}`)
	})
	for range 2 {
		if result, err := forge.Branches(ctx,
			&model.User{AccessToken: "token"}, &model.Repo{Owner: "octocat", Name: "repo"},
			&model.ListOptions{Page: 1, PerPage: 50},
		); err == nil || result != nil {
			t.Fatalf("secondary rate-limit must not look successful, got %v, %v", result, err)
		}
	}
	if requests.Load() != 1 {
		t.Fatalf("Retry-After cooldown ignored: %d requests", requests.Load())
	}
}
