package systemexec

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestRunInstallScriptDownloadRetries(t *testing.T) {
	for _, test := range []struct {
		name         string
		status       int
		failures     int
		wantAttempts int
		wantError    bool
	}{
		{"recover from server failure", 503, 1, 2, false},
		{"exhaust retries", 502, 3, 3, true},
		{"rate limit", 429, 1, 2, false},
		{"request timeout", 408, 1, 2, false},
		{"internal server error", 500, 1, 2, false},
		{"gateway timeout", 504, 1, 2, false},
		{"permanent failure", 404, 3, 1, true},
		{"unauthorized", 401, 3, 1, true},
		{"forbidden", 403, 3, 1, true},
		{"not implemented", 501, 3, 1, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attempts := 0
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				attempts++
				if attempts <= test.failures {
					w.WriteHeader(test.status)
					return
				}
				_, _ = io.WriteString(w, "printf script-ran")
			}))
			t.Cleanup(server.Close)
			var output, diagnostics bytes.Buffer
			_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
				URL: server.URL, Interpreter: []string{"sh"},
			}, &output, &diagnostics)
			if (err != nil) != test.wantError || attempts != test.wantAttempts {
				t.Fatalf("attempts = %d, error = %v", attempts, err)
			}
			if test.wantError {
				if !strings.Contains(err.Error(), "installer was not run") || strings.Contains(output.String(), "script-ran") {
					t.Fatalf("error = %v, output = %q", err, output.String())
				}
			} else if strings.Count(output.String(), "script-ran") != 1 {
				t.Fatalf("output = %q", output.String())
			}
			if !strings.Contains(diagnostics.String(), "attempt 1/3") || !strings.Contains(diagnostics.String(), "elapsed=") || !strings.Contains(diagnostics.String(), strconv.Itoa(test.status)) {
				t.Fatalf("missing diagnostics: %q", diagnostics.String())
			}
			if test.wantAttempts > 1 && !strings.Contains(output.String(), "Retrying download in 1s (attempt 2/3)") {
				t.Fatalf("missing retry progress: %q", output.String())
			}
			if test.wantAttempts == 3 && !strings.Contains(output.String(), "Retrying download in 2s (attempt 3/3)") {
				t.Fatalf("missing backoff progress: %q", output.String())
			}
		})
	}
}

type cancelOnRetryWriter struct {
	match  string
	cancel context.CancelFunc
}

func (w cancelOnRetryWriter) Write(p []byte) (int, error) {
	if strings.Contains(string(p), w.match) {
		w.cancel()
	}
	return len(p), nil
}

func TestRunInstallScriptCancelsBackoff(t *testing.T) {
	t.Parallel()
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(server.Close)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(ctx, ports.InstallScriptCommand{
		URL: server.URL, Interpreter: []string{"sh"},
	}, cancelOnRetryWriter{cancel: cancel, match: "Retrying download"}, io.Discard)
	if !errors.Is(err, context.Canceled) || attempts != 1 {
		t.Fatalf("attempts = %d, error = %v", attempts, err)
	}
}

func TestRunInstallScriptNeverRetriesExecution(t *testing.T) {
	t.Parallel()
	marker := filepath.Join(t.TempDir(), "executions")
	attempts := 0
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		_, _ = io.WriteString(w, "printf x >> \"$MARKER\"\nexit 7")
	}))
	t.Cleanup(server.Close)
	var diagnostics bytes.Buffer
	_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
		URL: server.URL, Interpreter: []string{"sh"}, Env: []string{"MARKER=" + marker},
	}, io.Discard, &diagnostics)
	content, readErr := os.ReadFile(marker)
	if err == nil || readErr != nil || string(content) != "x" || attempts != 1 {
		t.Fatalf("executions = %q, attempts = %d, error = %v, read = %v", content, attempts, err, readErr)
	}
	if !strings.Contains(err.Error(), "installer execution failed") || !strings.Contains(diagnostics.String(), "exit status 7") {
		t.Fatalf("error = %v, diagnostics = %q", err, diagnostics.String())
	}
}

func TestRunInstallScriptDownloadsExecutesAndCleansUp(t *testing.T) {
	t.Parallel()
	const body = "#!/bin/sh\nprintf 'vendor-script-ran'"
	server := httptest.NewTLSServer(httpHandler(body))
	t.Cleanup(server.Close)
	dataDir := t.TempDir()
	adapter := newAdapter(dataDir, server.Client())
	var output bytes.Buffer

	result, err := adapter.RunInstallScript(context.Background(), ports.InstallScriptCommand{
		URL: server.URL, Interpreter: []string{"sh"}, Env: []string{"CI=1"},
	}, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(output.String(), "vendor-script-ran") {
		t.Fatalf("output = %q", output.String())
	}
	sum := sha256.Sum256([]byte(body))
	if result.SHA256 != hex.EncodeToString(sum[:]) {
		t.Fatalf("sha256 = %q", result.SHA256)
	}
	entries, err := os.ReadDir(filepath.Join(dataDir, "installers", "tmp"))
	if err != nil || len(entries) != 0 {
		t.Fatalf("temporary scripts remain: %v, %v", entries, err)
	}
}

func TestRunInstallScriptPassesFixedArguments(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell installer")
	}
	server := httptest.NewTLSServer(httpHandler(`printf '%s|' "$@"`))
	t.Cleanup(server.Close)
	var output bytes.Buffer
	_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(t.Context(), ports.InstallScriptCommand{
		URL: server.URL, Interpreter: []string{"sh"}, Args: []string{"--commit", "abc 123", "--non-interactive"},
	}, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(output.String(), "--commit|abc 123|--non-interactive|") {
		t.Fatalf("installer arguments = %q", output.String())
	}
}

func TestPowerShellArgsKeepNamesAndQuoteValues(t *testing.T) {
	t.Parallel()
	got := powerShellArgs([]string{"-Commit", "818c13be", "-NonInteractive", "it's $(evil)", "-not a name"})
	if want := " -Commit '818c13be' -NonInteractive 'it''s $(evil)' '-not a name'"; got != want {
		t.Fatalf("powerShellArgs = %q, want %q", got, want)
	}
}

func TestRunInstallScriptPowerShellLoadsHashModule(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("Windows PowerShell installer")
	}
	shell, err := exec.LookPath("powershell.exe")
	if err != nil {
		t.Fatal("Windows PowerShell unavailable: ", err)
	}
	for _, tc := range []struct {
		name     string
		tail     string
		wantExit int
	}{
		{name: "success"},
		{name: "explicit unsuccessful exit", tail: "\nexit 7", wantExit: 7},
		{name: "terminating error", tail: "\nthrow 'synthetic installer failure'", wantExit: 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := "$PSModuleAutoLoadingPreference = 'None'\n(Get-FileHash -Path $PSCommandPath -Algorithm SHA256).Hash" + tc.tail
			server := httptest.NewTLSServer(httpHandler(body))
			t.Cleanup(server.Close)
			dataDir := t.TempDir()
			var output bytes.Buffer
			result, err := newAdapter(dataDir, server.Client()).RunInstallScript(t.Context(), ports.InstallScriptCommand{
				URL: server.URL, Interpreter: []string{shell, "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File"},
			}, &output, &output)
			if tc.wantExit == 0 {
				if err != nil {
					t.Fatalf("PowerShell installer failed: %v; output: %s", err, output.String())
				}
			} else {
				var exitErr *exec.ExitError
				if !errors.As(err, &exitErr) || exitErr.ExitCode() != tc.wantExit {
					t.Fatalf("PowerShell error = %v, want exit %d; output: %s", err, tc.wantExit, output.String())
				}
			}
			sum := sha256.Sum256([]byte(body))
			if result.SHA256 != hex.EncodeToString(sum[:]) || !strings.Contains(strings.ToLower(output.String()), result.SHA256) {
				t.Fatalf("installer hash = %q, output = %q, want %x", result.SHA256, output.String(), sum)
			}
			entries, readErr := os.ReadDir(filepath.Join(dataDir, "installers", "tmp"))
			if readErr != nil || len(entries) != 0 {
				t.Fatalf("temporary scripts remain: %v, %v", entries, readErr)
			}
		})
	}
}

func TestRunInstallScriptCleansUpAfterExecutionFailure(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(httpHandler("#!/bin/sh\nexit 7"))
	t.Cleanup(server.Close)
	dataDir := t.TempDir()

	_, err := newAdapter(dataDir, server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
		URL: server.URL, Interpreter: []string{"sh"},
	}, io.Discard, io.Discard)
	if err == nil {
		t.Fatal("expected execution failure")
	}
	entries, readErr := os.ReadDir(filepath.Join(dataDir, "installers", "tmp"))
	if readErr != nil || len(entries) != 0 {
		t.Fatalf("temporary scripts remain after failure: %v, %v", entries, readErr)
	}
}

func TestRunInstallScriptCancellationCleansUp(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(httpHandler("#!/bin/sh\nprintf started; sleep 5"))
	t.Cleanup(server.Close)
	dataDir := t.TempDir()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := newAdapter(dataDir, server.Client()).RunInstallScript(ctx, ports.InstallScriptCommand{
		URL: server.URL, Interpreter: []string{"sh"},
	}, cancelOnRetryWriter{cancel: cancel, match: "started"}, io.Discard)
	if err == nil {
		t.Fatal("expected cancellation")
	}
	entries, readErr := os.ReadDir(filepath.Join(dataDir, "installers", "tmp"))
	if (readErr != nil && !errors.Is(readErr, os.ErrNotExist)) || len(entries) != 0 {
		t.Fatalf("temporary scripts remain after cancellation: %v, %v", entries, readErr)
	}
}

func TestRunInstallScriptAllowsFiveHTTPSRedirects(t *testing.T) {
	t.Parallel()
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		step, _ := strconv.Atoi(r.URL.Query().Get("step"))
		if step < 5 {
			http.Redirect(w, r, "/?step="+strconv.Itoa(step+1), http.StatusFound)
			return
		}
		_, _ = io.WriteString(w, "#!/bin/sh\nprintf redirected")
	}))
	t.Cleanup(server.Close)
	var output bytes.Buffer

	_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
		URL: server.URL + "/?step=0", Interpreter: []string{"sh"},
	}, &output, &output)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(output.String(), "redirected") {
		t.Fatalf("output = %q", output.String())
	}
}

func TestRunInstallScriptRejectsTooManyOrInsecureRedirects(t *testing.T) {
	t.Parallel()
	t.Run("more than five redirects", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			step, _ := strconv.Atoi(r.URL.Query().Get("step"))
			http.Redirect(w, r, "/?step="+strconv.Itoa(step+1), http.StatusFound)
		}))
		t.Cleanup(server.Close)
		_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
			URL: server.URL + "/?step=0", Interpreter: []string{"sh"},
		}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "redirect limit") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("HTTPS redirect to HTTP", func(t *testing.T) {
		plain := httptest.NewServer(httpHandler("#!/bin/sh\nexit 0"))
		t.Cleanup(plain.Close)
		secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, plain.URL, http.StatusFound)
		}))
		t.Cleanup(secure.Close)
		_, err := newAdapter(t.TempDir(), secure.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
			URL: secure.URL, Interpreter: []string{"sh"},
		}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "redirect must remain HTTPS") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestRunInstallScriptRejectsUnsafeDownloads(t *testing.T) {
	t.Parallel()
	t.Run("plain HTTP", func(t *testing.T) {
		server := httptest.NewServer(httpHandler("#!/bin/sh\nexit 0"))
		t.Cleanup(server.Close)
		_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
			URL: server.URL, Interpreter: []string{"sh"},
		}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(err.Error(), "absolute HTTPS") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("oversized response", func(t *testing.T) {
		server := httptest.NewTLSServer(httpHandler(strings.Repeat("x", remoteScriptMaxBytes+1)))
		t.Cleanup(server.Close)
		_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
			URL: server.URL, Interpreter: []string{"sh"},
		}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "download limit") {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("non-success status", func(t *testing.T) {
		server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", http.StatusBadGateway)
		}))
		t.Cleanup(server.Close)
		_, err := newAdapter(t.TempDir(), server.Client()).RunInstallScript(context.Background(), ports.InstallScriptCommand{
			URL: server.URL, Interpreter: []string{"sh"},
		}, io.Discard, io.Discard)
		if err == nil || !strings.Contains(errors.Unwrap(err).Error(), "502 Bad Gateway") {
			t.Fatalf("error = %v", err)
		}
	})
}

func TestNewUsesAODataDir(t *testing.T) {
	t.Parallel()
	dataDir := t.TempDir()
	if got, want := New(dataDir).installerRoot, filepath.Join(dataDir, "installers", "tmp"); got != want {
		t.Fatalf("installerRoot = %q, want %q", got, want)
	}
}

func httpHandler(body string) *staticHTTPHandler {
	return &staticHTTPHandler{body: body}
}

type staticHTTPHandler struct {
	body string
}

func (h *staticHTTPHandler) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	_, _ = io.WriteString(w, h.body)
}

// A transport double makes connection failures deterministic without external networking.
type installerRoundTripper func(*http.Request) (*http.Response, error)

func (f installerRoundTripper) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestRunInstallScriptTransportFailures(t *testing.T) {
	for _, test := range []struct {
		name         string
		err          error
		wantAttempts int
	}{
		{"TLS timeout", &net.DNSError{Err: "TLS handshake timeout", IsTimeout: true}, 3},
		{"temporary DNS failure", &net.DNSError{Err: "server misbehaving", IsTemporary: true}, 3},
		{"truncated download", io.ErrUnexpectedEOF, 3},
		{"invalid certificate", x509.UnknownAuthorityError{}, 1},
		{"canceled", context.Canceled, 1},
		{"unknown host", &net.DNSError{Err: "no such host", IsNotFound: true}, 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			attempts := 0
			client := &http.Client{Transport: installerRoundTripper(func(*http.Request) (*http.Response, error) {
				attempts++
				return nil, test.err
			})}
			var diagnostics bytes.Buffer
			_, err := newAdapter(t.TempDir(), client).RunInstallScript(context.Background(), ports.InstallScriptCommand{
				URL: "https://installer.invalid/install.sh", Interpreter: []string{"sh"},
			}, io.Discard, &diagnostics)
			if err == nil || attempts != test.wantAttempts || !errors.Is(err, test.err) {
				t.Fatalf("attempts = %d, error = %v", attempts, err)
			}
			if !strings.Contains(diagnostics.String(), test.err.Error()) {
				t.Fatalf("missing cause: %q", diagnostics.String())
			}
		})
	}
}

type installerFailedReader struct{}

func (installerFailedReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func TestRunInstallScriptDiscardsPartialDownloadBeforeRetry(t *testing.T) {
	t.Parallel()
	attempts := 0
	client := &http.Client{Transport: installerRoundTripper(func(*http.Request) (*http.Response, error) {
		attempts++
		var body io.Reader = strings.NewReader("printf complete-script")
		if attempts == 1 {
			body = io.MultiReader(strings.NewReader("printf partial-script"), installerFailedReader{})
		}
		return &http.Response{StatusCode: http.StatusOK, Body: io.NopCloser(body)}, nil
	})}
	var output bytes.Buffer
	_, err := newAdapter(t.TempDir(), client).RunInstallScript(context.Background(), ports.InstallScriptCommand{
		URL: "https://installer.invalid/install.sh", Interpreter: []string{"sh"},
	}, &output, io.Discard)
	if err != nil || attempts != 2 || !strings.HasSuffix(output.String(), "complete-script") || strings.Contains(output.String(), "partial-script") {
		t.Fatalf("attempts = %d, output = %q, error = %v", attempts, output.String(), err)
	}
}

func TestInstallerDownloadBackoff(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		started := time.Now()
		var attempts []time.Duration
		client := &http.Client{Transport: installerRoundTripper(func(*http.Request) (*http.Response, error) {
			attempts = append(attempts, time.Since(started))
			return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Body: io.NopCloser(strings.NewReader(""))}, nil
		})}
		_, err := newAdapter(t.TempDir(), client).downloadInstallScript(context.Background(), "https://installer.invalid/install.sh", io.Discard, io.Discard)
		if err == nil || len(attempts) != 3 || attempts[0] != 0 || attempts[1] != time.Second || attempts[2] != 3*time.Second {
			t.Fatalf("attempt times = %v, error = %v", attempts, err)
		}
	})
}

func TestInstallerDownloadTimeoutBounds(t *testing.T) {
	for _, test := range []struct {
		name          string
		parentTimeout time.Duration
		wantAttempts  int
		wantElapsed   time.Duration
	}{
		{"per-attempt deadlines", 2 * time.Minute, 3, 93 * time.Second},
		{"parent deadline stops retries", 5 * time.Second, 1, 5 * time.Second},
	} {
		t.Run(test.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), test.parentTimeout)
				defer cancel()
				started := time.Now()
				attempts := 0
				client := &http.Client{Transport: installerRoundTripper(func(req *http.Request) (*http.Response, error) {
					attempts++
					<-req.Context().Done()
					return nil, req.Context().Err()
				})}
				_, err := newAdapter(t.TempDir(), client).downloadInstallScript(ctx, "https://installer.invalid/install.sh", io.Discard, io.Discard)
				if !errors.Is(err, context.DeadlineExceeded) || attempts != test.wantAttempts || time.Since(started) != test.wantElapsed {
					t.Fatalf("attempts = %d, elapsed = %s, error = %v", attempts, time.Since(started), err)
				}
			})
		})
	}
}
