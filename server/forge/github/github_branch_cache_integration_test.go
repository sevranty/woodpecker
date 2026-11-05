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

	"github.com/google/go-github/v93/github"
	"github.com/prometheus/client_golang/prometheus/testutil"
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

func TestGitHubBranchCooldownWaitsForBothHeaders(t *testing.T) {
	tests := []struct {
		name       string
		status     int
		resetAfter time.Duration
		retryAfter string
		minimum    time.Duration
	}{
		{name: "primary reset later", status: http.StatusForbidden, resetAfter: 95 * time.Second, retryAfter: "5", minimum: 90 * time.Second},
		{name: "retry after later", status: http.StatusTooManyRequests, resetAfter: 10 * time.Second, retryAfter: "95", minimum: 95 * time.Second},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			headers := make(http.Header)
			headers.Set("Retry-After", tt.retryAfter)
			headers.Set("X-RateLimit-Remaining", "0")
			headers.Set("X-RateLimit-Reset", strconv.FormatInt(time.Now().Add(tt.resetAfter).Unix(), 10))
			resp := &github.Response{Response: &http.Response{StatusCode: tt.status, Header: headers}}
			if got := branchCooldown(resp, fmt.Errorf("rate limit")); got < tt.minimum {
				t.Fatalf("cooldown %s is shorter than the required %s", got, tt.minimum)
			}
		})
	}
	permission := &github.Response{Response: &http.Response{StatusCode: http.StatusForbidden, Header: make(http.Header)}}
	if got := branchCooldown(permission, fmt.Errorf("permission denied")); got != 0 {
		t.Fatalf("ordinary permission 403 was incorrectly delayed: %s", got)
	}
}

func TestGitHubOutboundMetricsCountActualHTTP(t *testing.T) {
	var actualRequests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		actualRequests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-RateLimit-Resource", "core")
		w.Header().Set("X-RateLimit-Limit", "5000")
		w.Header().Set("X-RateLimit-Used", "9")
		if r.URL.Path == "/repos/octocat/denied/branches" {
			w.Header().Set("X-RateLimit-Remaining", "0")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprint(w, `{"message":"rate limit exceeded"}`)
			return
		}
		w.Header().Set("X-RateLimit-Remaining", "4991")
		switch {
		case r.URL.Path == "/repos/octocat/repo/branches" && r.Method == http.MethodGet:
			_, _ = fmt.Fprint(w, `[{"name":"main"}]`)
		case r.URL.Path == "/repos/octocat/repo/statuses/abc123" && r.Method == http.MethodPost:
			w.WriteHeader(http.StatusCreated)
			_, _ = fmt.Fprint(w, `{"id":1,"state":"success"}`)
		default:
			t.Errorf("unexpected endpoint or method: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(upstream.Close)

	forge := &client{API: upstream.URL + "/"}
	ctx := context.Background()
	tokenA := "test-only-credential-A"
	tokenB := "test-only-credential-B"
	aliasA := githubMetricIdentity(tokenA)
	aliasB := githubMetricIdentity(tokenB)
	if aliasA == aliasB || aliasA == tokenA || aliasB == tokenB || aliasA == "overflow" || aliasB == "overflow" {
		t.Fatalf("credential metrics alias is not distinct and protected: %q, %q", aliasA, aliasB)
	}
	apiA, err := forge.newClientToken(ctx, tokenA)
	if err != nil {
		t.Fatal(err)
	}
	apiB, err := forge.newClientToken(ctx, tokenB)
	if err != nil {
		t.Fatal(err)
	}

	beforeGet := testutil.ToFloat64(githubHTTPRequests.WithLabelValues(aliasA, "GET", "branches", "200"))
	beforePost := testutil.ToFloat64(githubHTTPRequests.WithLabelValues(aliasA, "POST", "statuses", "201"))
	before403 := testutil.ToFloat64(githubHTTPRequests.WithLabelValues(aliasB, "GET", "branches", "403"))

	// Two real HTTP GETs from the same credential have two RoundTrips.
	for range 2 {
		branches, _, callErr := apiA.Repositories.ListBranches(ctx, "octocat", "repo", &github.BranchListOptions{Page: 1})
		if callErr != nil || len(branches) != 1 || branches[0].GetName() != "main" {
			t.Fatalf("unexpected branch fetch: %v / %v", branches, callErr)
		}
	}
	_, _, err = apiA.Repositories.CreateStatus(ctx, "octocat", "repo", "abc123", github.RepoStatus{
		State: new("success"),
	})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = apiB.Repositories.ListBranches(ctx, "octocat", "denied", &github.BranchListOptions{Page: 1})
	if err == nil {
		t.Fatal("rate-limited upstream response must remain an error")
	}

	if got := actualRequests.Load(); got != 4 {
		t.Fatalf("expected 4 real upstream requests, got %d", got)
	}
	if got := testutil.ToFloat64(githubHTTPRequests.WithLabelValues(aliasA, "GET", "branches", "200")) - beforeGet; got != 2 {
		t.Fatalf("GET counter=%v, expected 2", got)
	}
	if got := testutil.ToFloat64(githubHTTPRequests.WithLabelValues(aliasA, "POST", "statuses", "201")) - beforePost; got != 1 {
		t.Fatalf("POST status counter=%v, expected 1", got)
	}
	if got := testutil.ToFloat64(githubHTTPRequests.WithLabelValues(aliasB, "GET", "branches", "403")) - before403; got != 1 {
		t.Fatalf("403 counter=%v, expected 1", got)
	}
	if remaining := testutil.ToFloat64(githubHTTPQuota.WithLabelValues(aliasB, "core", "remaining")); remaining != 0 {
		t.Fatalf("quota remaining=%v, expected 0", remaining)
	}
	if used := testutil.ToFloat64(githubHTTPQuota.WithLabelValues(aliasA, "core", "used")); used != 9 {
		t.Fatalf("quota used=%v, expected 9", used)
	}
	if got := githubMetricEndpoint("/repos/private-owner/secret-repo/statuses/secret-sha"); got != "statuses" {
		t.Fatalf("endpoint classification exposes raw details: %q", got)
	}
}
