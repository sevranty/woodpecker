package github

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func TestWFO882AllowlistExact(t *testing.T) {
	for _, repo := range []string{"telegram-bot-hub", "servers", "agent-execution-fabric", "web-factory-os"} {
		if !wfo882AllowedStatusRepository("sevranty", repo) {
			t.Errorf("allowed repo rejected: %s", repo)
		}
	}
	for _, pair := range [][2]string{
		{"other", "servers"},
		{"sevranty", "lifeops"},
		{"sevranty", "../servers"},
		{"Sevranty", "servers"},
		{"sevranty", "servers/extra"},
		{"", "servers"},
	} {
		if wfo882AllowedStatusRepository(pair[0], pair[1]) {
			t.Errorf("unauthorized repo accepted: %v", pair)
		}
	}
}

func TestWFO882DefaultAndPartialAdmissionFailClosed(t *testing.T) {
	tests := []struct {
		name string
		vars map[string]string
		good bool
	}{
		{name: "default-disabled", vars: nil, good: true},
		{name: "enabled-without-approval", vars: map[string]string{wfo882EnabledKey: "1"}},
		{name: "file-only", vars: map[string]string{wfo882TokenFileKey: "/secure/token"}},
		{name: "no-compliance", vars: map[string]string{
			wfo882EnabledKey: "1", wfo882OwnerKey: "1", wfo882TokenFileKey: "/secure/token",
		}},
		{name: "all-guards", vars: map[string]string{
			wfo882EnabledKey: "1", wfo882OwnerKey: "1",
			wfo882ComplianceKey: "1", wfo882TokenFileKey: "/secure/token",
		}, good: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			getenv := func(key string) string { return tc.vars[key] }
			readToken := func(path string) (string, error) {
				calls++
				if path != "/secure/token" {
					t.Fatal("unexpected file path")
				}
				return "test-only-secondary", nil
			}
			route, err := wfo882LoadStatusRoute(getenv, readToken)
			if tc.good && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !tc.good && err == nil {
				t.Fatal("incomplete admission accepted")
			}
			if tc.name == "default-disabled" && (route != nil || calls != 0) {
				t.Fatal("default path read token")
			}
			if !tc.good && calls != 0 {
				t.Fatal("incomplete admission read token")
			}
			if tc.name == "all-guards" && (route == nil || calls != 1) {
				t.Fatal("admitted route not loaded")
			}
		})
	}
}

func TestWFO882ProtectedFileAndSymlink(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "token")
	if err := os.WriteFile(path, []byte("test-only-secret\n"), 0600); err != nil {
		t.Fatal(err)
	}
	token, err := wfo882ReadProtectedToken(path)
	if err != nil || token != "test-only-secret" {
		t.Fatalf("private file rejected: %v", err)
	}
	if err := os.Chmod(path, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := wfo882ReadProtectedToken(path); err == nil {
		t.Fatal("world-readable secret accepted")
	}
	if err := os.Chmod(path, 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "symlink")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if _, err := wfo882ReadProtectedToken(link); err == nil {
		t.Fatal("symlink secret accepted")
	}
	if _, err := wfo882ReadProtectedToken("relative"); err == nil {
		t.Fatal("relative secret path accepted")
	}
}

func TestWFO882SelectTokenNoFallback(t *testing.T) {
	route := &wfo882StatusRoute{token: "only50bw-test"}
	calls := 0
	verify := func(token string) error {
		calls++
		if token != "only50bw-test" {
			t.Fatal("unexpected credential passed")
		}
		return nil
	}
	for range 2 {
		token, err := route.selectToken("sevranty", "servers", "existing", verify)
		if err != nil || token != "only50bw-test" {
			t.Fatalf("wrong identity returned: %q, %v", token, err)
		}
	}
	if calls != 1 {
		t.Fatalf("verification called %d times", calls)
	}
	token, err := route.selectToken("sevranty", "lifeops", "existing", verify)
	if err != nil || token != "existing" || calls != 1 {
		t.Fatal("non-target repo switched identity")
	}
}

func TestWFO882RejectedVerificationStaysBlocked(t *testing.T) {
	route := &wfo882StatusRoute{token: "secret-sentinel"}
	verify := func(token string) error { return errors.New("HTTP 403: secret-sentinel") }
	for range 2 {
		token, err := route.selectToken("sevranty", "servers", "existing", verify)
		if token != "" || err == nil || strings.Contains(err.Error(), "sentinel") {
			t.Fatalf("verification failure leaked token or fell back: %q, %v", token, err)
		}
	}
}

func TestWFO882ConcurrentVerificationSingleCall(t *testing.T) {
	route := &wfo882StatusRoute{token: "only50bw-test"}
	var calls atomic.Int32
	var wg sync.WaitGroup
	for range 30 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			token, err := route.selectToken("sevranty", "web-factory-os", "existing", func(token string) error {
				calls.Add(1)
				return nil
			})
			if err != nil || token != "only50bw-test" {
				t.Errorf("concurrent route: %q, %v", token, err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("expected single verification, got %d", calls.Load())
	}
}

func TestWFO882InvalidCredentialNeverReadFromProcessEnv(t *testing.T) {
	flags := map[string]string{
		wfo882EnabledKey: "1", wfo882OwnerKey: "1",
		wfo882ComplianceKey: "1", wfo882TokenFileKey: "/secure/token",
	}
	for _, token := range []string{"", "bad token", "bad\r\ntoken"} {
		_, err := wfo882LoadStatusRoute(
			func(k string) string { return flags[k] },
			func(string) (string, error) { return token, nil },
		)
		if err == nil || strings.Contains(err.Error(), token) {
			t.Fatalf("invalid token accepted or disclosed: %q", token)
		}
	}
}
