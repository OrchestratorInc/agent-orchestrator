package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestGHProxyClaimsOnceAndPreservesStreams(t *testing.T) {
	for _, status := range []int{200, 409, 502} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			cfg := setConfigEnv(t)
			t.Setenv("AO_SESSION_ID", "workspace-4")
			t.Setenv("AO_REVIEW_SESSION_ID", "")
			dir := t.TempDir()
			t.Setenv("AO_DATA_DIR", dir)
			claims := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/api/v1/sessions/workspace-4/pr/claim" {
					t.Errorf("unexpected request: %s", r.URL.Path)
				}
				claims++
				var req claimPRRequest
				if json.NewDecoder(r.Body).Decode(&req) != nil || req.PR != "https://github.com/example/frontend/pull/123" || req.AllowTakeover {
					t.Errorf("claim=%+v", req)
				}
				w.WriteHeader(status)
				if status != 200 {
					_, _ = io.WriteString(w, `{"code":"PR_CLAIM_FAILED","message":"cannot claim","requestId":"proxy-request"}`)
				}
			}))
			defer srv.Close()
			writeRunFileFor(t, cfg, srv)
			var out, errOut bytes.Buffer
			args := []string{"pr", "create", "--repo", "example/frontend", "--body", "a body\nwith $special characters"}
			c := commandContext{deps: Deps{In: strings.NewReader("input"), Out: &out, Err: &errOut, ProcessAlive: func(int) bool { return true }, RunInteractiveCommand: func(_ context.Context, name string, got []string, in io.Reader, stdout, stderr io.Writer) error {
				if name != "real-gh" || !reflect.DeepEqual(got, args) {
					t.Errorf("forwarded %s %q", name, got)
				}
				b, _ := io.ReadAll(in)
				if string(b) != "input" {
					t.Errorf("stdin=%s", b)
				}
				_, _ = io.WriteString(stdout, "https://github.com/example/frontend/pull/123\n")
				_, _ = io.WriteString(stderr, "gh diagnostic\n")
				return nil
			}}.withDefaults()}
			if code := c.runGH(context.Background(), "real-gh", args); code != 0 {
				t.Fatalf("exit=%d", code)
			}
			if claims != 1 || out.String() != "https://github.com/example/frontend/pull/123\n" {
				t.Fatalf("claims=%d stdout=%s", claims, &out)
			}
			if status == 200 {
				if errOut.String() != "gh diagnostic\n" {
					t.Fatal(errOut.String())
				}
			} else {
				log, err := os.ReadFile(filepath.Join(dir, hooksLogName))
				if err != nil || !strings.Contains(string(log), "proxy-request") || !strings.Contains(errOut.String(), "PR created") {
					t.Fatalf("log=%s err=%v stderr=%s", log, err, &errOut)
				}
			}
		})
	}
}

func TestGHProxyDoesNotClaimUnrelatedOrFailedCommands(t *testing.T) {
	for _, tt := range []struct {
		name                    string
		args                    []string
		stdout, session, review string
		fail                    bool
	}{
		{"view", []string{"pr", "view"}, "https://github.com/example/frontend/pull/123", "worker-1", "", false},
		{"dry run", []string{"pr", "create", "--dry-run"}, "https://github.com/example/frontend/pull/123", "worker-1", "", false},
		{"reviewer", []string{"pr", "create"}, "https://github.com/example/frontend/pull/123", "worker-1", "review-1", false},
		{"no session", []string{"pr", "create"}, "https://github.com/example/frontend/pull/123", "", "", false},
		{"multiple URLs", []string{"pr", "create"}, "https://github.com/example/frontend/pull/123\nhttps://github.com/example/frontend/pull/456", "worker-1", "", false},
		{"untrusted host", []string{"pr", "create"}, "https://github.com.evil.test/example/frontend/pull/123", "worker-1", "", false},
		{"failed create", []string{"pr", "create"}, "https://github.com/example/frontend/pull/123", "worker-1", "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cfg := setConfigEnv(t)
			server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected registration") }))
			defer server.Close()
			writeRunFileFor(t, cfg, server)
			t.Setenv("AO_SESSION_ID", tt.session)
			t.Setenv("AO_REVIEW_SESSION_ID", tt.review)
			var out, errOut bytes.Buffer
			c := commandContext{deps: Deps{Out: &out, Err: &errOut, ProcessAlive: func(int) bool { return true }, HTTPClient: &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
				t.Error("unexpected daemon request")
				return nil, context.Canceled
			})}, RunInteractiveCommand: func(_ context.Context, _ string, _ []string, _ io.Reader, stdout, _ io.Writer) error {
				_, _ = io.WriteString(stdout, tt.stdout)
				if tt.fail {
					return context.Canceled
				}
				return nil
			}}.withDefaults()}
			code := c.runGH(context.Background(), "gh", tt.args)
			if (code != 0) != tt.fail || out.String() != tt.stdout {
				t.Fatalf("code=%d stdout=%q", code, out.String())
			}
		})
	}
}

func TestGHCreateArguments(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want bool
	}{
		{[]string{"pr", "create", "--fill"}, true},
		{[]string{"--repo", "o/r", "pr", "create"}, true},
		{[]string{"-Ro/r", "pr", "create"}, true},
		{[]string{"--repo=o/r", "pr", "create"}, true},
		{[]string{"--repo"}, false},
		{[]string{"alias", "set", "new", "pr create"}, false},
		{[]string{"pr", "create", "--web"}, false},
		{[]string{"pr", "create", "--help"}, false},
	} {
		if got := ghCreatesPR(tt.args); got != tt.want {
			t.Errorf("%q = %v", tt.args, got)
		}
	}
}

func TestGHProxyTimeoutIsNotRetried(t *testing.T) {
	cfg := setConfigEnv(t)
	t.Setenv("AO_DATA_DIR", t.TempDir())
	srv := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Error("unexpected real request") }))
	defer srv.Close()
	writeRunFileFor(t, cfg, srv)
	calls := 0
	var errOut bytes.Buffer
	c := commandContext{deps: Deps{Err: &errOut, ProcessAlive: func(int) bool { return true }, HTTPClient: &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		calls++
		deadline, ok := r.Context().Deadline()
		if !ok || time.Until(deadline) > 5*time.Second {
			t.Error("missing bounded deadline")
		}
		return nil, context.DeadlineExceeded
	})}}.withDefaults()}
	c.registerGHPR(context.Background(), "worker-1", "https://github.com/example/frontend/pull/123")
	if calls != 1 || !strings.Contains(errOut.String(), "deadline exceeded") {
		t.Fatalf("calls=%d stderr=%s", calls, &errOut)
	}
}

func TestGHOutputBoundedWithoutTruncatingForwardedOutput(t *testing.T) {
	var captured ghOutput
	var forwarded bytes.Buffer
	input := strings.Repeat("x", 10000)
	if _, err := io.WriteString(io.MultiWriter(&forwarded, &captured), input); err != nil {
		t.Fatal(err)
	}
	if forwarded.String() != input || captured.Len() != 4096 || !captured.overflow {
		t.Fatal("output bound or forwarding violated")
	}
}

func TestGHProxyPreservesProcessExitCode(t *testing.T) {
	if os.Getenv("AO_TEST_GH_EXIT") == "1" {
		os.Exit(37)
	}
	t.Setenv("AO_TEST_GH_EXIT", "1")
	t.Setenv("AO_SESSION_ID", "worker-1")
	t.Setenv("AO_REVIEW_SESSION_ID", "")
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	c := commandContext{deps: Deps{Out: &out, Err: &errOut, RunInteractiveCommand: func(ctx context.Context, _ string, _ []string, in io.Reader, stdout, stderr io.Writer) error {
		return runGHCommand(ctx, exe, []string{"-test.run=^TestGHProxyPreservesProcessExitCode$"}, in, stdout, stderr)
	}}.withDefaults()}
	if code := c.runGH(context.Background(), "gh", []string{"pr", "create"}); code != 37 {
		t.Fatalf("code=%d stderr=%s", code, &errOut)
	}
	if errOut.Len() != 0 {
		t.Fatalf("added stderr: %s", &errOut)
	}
}
