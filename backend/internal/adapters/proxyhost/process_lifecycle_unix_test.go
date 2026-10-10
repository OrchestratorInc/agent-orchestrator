//go:build !windows

package proxyhost

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// This subprocess models the helper control contract without any provider
// credentials. It exercises the real detached-process ownership boundary.
func TestProviderHostProcessFixture(t *testing.T) {
	if os.Getenv("AO_ACCOUNT_PROCESS_FIXTURE") != "1" {
		return
	}
	var root, port string
	for i, arg := range os.Args {
		if i+1 < len(os.Args) {
			switch arg {
			case "--data-dir":
				root = os.Args[i+1]
			case "--port":
				port = os.Args[i+1]
			}
		}
	}
	if root == "" || port == "" {
		os.Exit(2)
	}
	for _, name := range []string{"AO_PROXY_CONTROL_KEY", "AO_PROXY_INFERENCE_KEY"} {
		if len(os.Getenv(name)) != 64 {
			os.Exit(3)
		}
	}
	if os.Getenv("WRITABLE_PATH") != root || os.Getenv("MANAGEMENT_PASSWORD") != "" {
		os.Exit(4)
	}
	args, _ := json.Marshal(os.Args)
	if err := os.WriteFile(filepath.Join(root, "fixture-args.json"), args, 0o600); err != nil {
		os.Exit(5)
	}
	mode := os.Getenv("AO_ACCOUNT_PROCESS_MODE")
	if mode == "delay" {
		time.Sleep(300 * time.Millisecond)
	}
	var mu sync.Mutex
	var routes ports.ProviderRouteSnapshot
	listener, err := net.Listen("tcp", "127.0.0.1:"+port)
	if err != nil {
		os.Exit(6)
	}
	server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+os.Getenv("AO_PROXY_CONTROL_KEY") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/ao/status":
			protocol := 2
			if mode == "incompatible" {
				protocol = 3
			}
			_ = json.NewEncoder(w).Encode(map[string]int{"protocol_version": protocol})
		case "/ao/routes":
			mu.Lock()
			defer mu.Unlock()
			if err := json.NewDecoder(r.Body).Decode(&routes); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			_ = json.NewEncoder(w).Encode(routes)
		default:
			http.NotFound(w, r)
		}
	})}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	go func() { <-stop; _ = server.Close() }()
	_ = server.Serve(listener)
	os.Exit(0)
}

func processClient(t *testing.T, mode string) *Client {
	t.Helper()
	t.Setenv("AO_ACCOUNT_PROCESS_FIXTURE", "1")
	t.Setenv("AO_ACCOUNT_PROCESS_MODE", mode)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsAny(executable, "'\n") {
		t.Fatal("test executable path cannot be quoted safely")
	}
	root := t.TempDir()
	wrapper := filepath.Join(root, "ao-proxy-host")
	script := "#!/bin/sh\nexec '" + executable + "' -test.run='^TestProviderHostProcessFixture$' -- \"$@\"\n"
	if err = os.WriteFile(wrapper, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	c, err := New(root, wrapper)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if c.state.PID > 0 && c.state.PID != os.Getpid() {
			_ = syscall.Kill(c.state.PID, syscall.SIGTERM)
			waitProcessGone(t, c.state.PID)
		}
	})
	return c
}
func waitProcessGone(t *testing.T, pid int) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		dead, err := processDead(pid)
		if dead && err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Errorf("temporary helper process %d did not exit", pid)
}
func readManifest(t *testing.T, c *Client) manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(c.root, "run", "host.json"))
	if err != nil {
		t.Fatal(err)
	}
	var state manifest
	if err = json.Unmarshal(data, &state); err != nil {
		t.Fatal(err)
	}
	return state
}
func TestProviderHostDetachedLaunchPersistsOwnedIdentity(t *testing.T) {
	c := processClient(t, "")
	original := c.state
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.state.PID <= 0 || c.state.PID == os.Getpid() {
		t.Fatal("no separate helper process")
	}
	if original.Port != c.state.Port || original.ControlKey != c.state.ControlKey || original.InferenceKey != c.state.InferenceKey || original.TicketKey != c.state.TicketKey {
		t.Fatal("launch changed a session's stable private identity")
	}
	if saved := readManifest(t, c); saved != c.state {
		t.Fatal("persisted helper ownership does not match running process")
	}
	data, err := os.ReadFile(filepath.Join(c.root, "fixture-args.json"))
	if err != nil {
		t.Fatal(err)
	}
	var args []string
	if err = json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	wantTail := []string{"--data-dir", c.root, "--port", strconv.Itoa(c.state.Port)}
	if len(args) < 4 || !reflect.DeepEqual(args[len(args)-4:], wantTail) {
		t.Fatalf("launch arguments=%v", args)
	}
	for _, key := range []string{c.state.ControlKey, c.state.InferenceKey, c.state.TicketKey} {
		if strings.Contains(string(data), key) {
			t.Fatal("private identity appeared in process arguments")
		}
	}
	for _, path := range []string{"run/host.json", "logs/host.log", "fixture-args.json"} {
		stat, err := os.Stat(filepath.Join(c.root, path))
		if err != nil {
			t.Fatal(err)
		}
		if stat.Mode().Perm() != 0o600 {
			t.Fatalf("private file %s permissions=%v", path, stat.Mode())
		}
	}
}
func TestProviderHostDaemonReplacementReusesDetachedProcess(t *testing.T) {
	c := processClient(t, "")
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	firstPID := c.state.PID
	// The replacement daemon deliberately has no executable available. Reuse
	// proves it needs neither a child-process handle nor a new account ticket.
	replacement, err := New(c.root, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = replacement.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if replacement.state.PID != firstPID || replacement.Endpoint() != c.Endpoint() {
		t.Fatal("daemon replacement changed helper identity")
	}
	oldKey, _ := c.TicketKey()
	newKey, _ := replacement.TicketKey()
	if !reflect.DeepEqual(oldKey, newKey) {
		t.Fatal("daemon replacement invalidated session ticket key")
	}
	next := ports.ProviderRouteSnapshot{Revision: 1, Routes: []ports.ProviderRoute{{SessionID: domain.SessionID("existing-conversation"), TicketHash: strings.Repeat("a", 64), Provider: "codex", AuthID: "account-a"}}}
	if err = replacement.ApplyRoutes(context.Background(), next); err != nil {
		t.Fatal(err)
	}
	if err = c.ApplyRoutes(context.Background(), next); err != nil {
		t.Fatal("old daemon client cannot use surviving helper:", err)
	}
	if saved := readManifest(t, c); saved.PID != firstPID {
		t.Fatal("reuse rewrote ownership")
	}
}
func TestProviderHostConfirmedExitRestartsOnSameEndpoint(t *testing.T) {
	c := processClient(t, "")
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	first := c.state
	if err := syscall.Kill(first.PID, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	waitProcessGone(t, first.PID)
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if c.state.PID == first.PID || c.state.PID <= 0 {
		t.Fatal("dead helper was not replaced")
	}
	if c.state.Port != first.Port || c.state.TicketKey != first.TicketKey || c.state.ControlKey != first.ControlKey {
		t.Fatal("restart invalidated existing session routes")
	}
	if saved := readManifest(t, c); saved != c.state {
		t.Fatal("restart did not persist new ownership")
	}
}
func TestProviderHostCancelledReadinessWaitKeepsOwnedProcess(t *testing.T) {
	c := processClient(t, "delay")
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := c.Ensure(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("readiness cancellation=%v", err)
	}
	pid := c.state.PID
	if pid <= 0 || readManifest(t, c).PID != pid {
		t.Fatal("cancelled caller lost detached process ownership")
	}
	deadline := time.Now().Add(3 * time.Second)
	for c.probe(context.Background()) != nil {
		if time.Now().After(deadline) {
			t.Fatal("detached helper never completed delayed startup")
		}
		time.Sleep(10 * time.Millisecond)
	}
	replacement, err := New(c.root, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = replacement.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if replacement.state.PID != pid {
		t.Fatal("cancelled readiness caused a second helper")
	}
}
func TestProviderHostIncompatibleLiveProcessRemainsUntouched(t *testing.T) {
	c := processClient(t, "incompatible")
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	// A new helper with a different protocol cannot be acknowledged by this
	// daemon. Cancellation keeps this fixture test bounded without killing it.
	if err := c.Ensure(ctx); err == nil {
		t.Fatal("incompatible helper accepted")
	}
	pid := c.state.PID
	if pid <= 0 {
		t.Fatal("fixture did not start")
	}
	deadline := time.Now().Add(5 * time.Second)
	for !errors.Is(c.probe(context.Background()), errProtocol) {
		if time.Now().After(deadline) {
			t.Fatal("incompatible helper did not finish starting")
		}
		time.Sleep(10 * time.Millisecond)
	}
	replacement, err := New(c.root, c.binary)
	if err != nil {
		t.Fatal(err)
	}
	if err = replacement.Ensure(context.Background()); !errors.Is(err, errProtocol) {
		t.Fatalf("replacement protocol error=%v", err)
	}
	if replacement.state.PID != pid {
		t.Fatal("incompatible live helper was replaced")
	}
	dead, err := processDead(pid)
	if err != nil || dead {
		t.Fatal("protocol check stopped the active helper")
	}
}
func TestProviderHostOccupiedEndpointCannotBeClaimed(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusServiceUnavailable} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			c := processClient(t, "")
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.state.Port))
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(status) })}
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close() })
			before := readManifest(t, c)
			if err = c.Ensure(context.Background()); err == nil {
				t.Fatal("unknown endpoint was adopted")
			}
			if c.state.PID != 0 || readManifest(t, c) != before {
				t.Fatal("unknown endpoint caused an unowned helper launch")
			}
			if _, err = os.Stat(filepath.Join(c.root, "fixture-args.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("helper process was launched onto an occupied endpoint")
			}
		})
	}
}
func TestProviderHostLiveOwnerWithFailedProbeRefusesTakeover(t *testing.T) {
	c := processClient(t, "")
	c.state.PID = os.Getpid()
	if err := c.save(); err != nil {
		t.Fatal(err)
	}
	before := readManifest(t, c)
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "ownership is unverified") {
		t.Fatalf("ownership refusal=%v", err)
	}
	if c.state != before || readManifest(t, c) != before {
		t.Fatal("failed probe rewrote live ownership")
	}
	if _, err := os.Stat(filepath.Join(c.root, "fixture-args.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unresponsive live owner was replaced")
	}
	// The fixture cleanup must never signal the test runner used as an owner.
	c.state.PID = 0
}
func TestProviderHostMissingExecutableLeavesPrivateIdentityReusable(t *testing.T) {
	c := processClient(t, "")
	c.binary = filepath.Join(c.root, "missing-binary")
	before := readManifest(t, c)
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "start proxy helper") {
		t.Fatalf("missing executable=%v", err)
	}
	if c.state.PID != 0 || readManifest(t, c) != before {
		t.Fatal("failed launch changed stable identity")
	}
}

func TestProviderHostAlreadyCancelledEnsureCannotLaunchHelper(t *testing.T) {
	c := processClient(t, "")
	before := readManifest(t, c)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := c.Ensure(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled ensure=%v", err)
	}
	if c.state.PID != 0 || readManifest(t, c) != before {
		t.Fatal("cancelled caller launched an owned process")
	}
	if _, err := os.Stat(filepath.Join(c.root, "fixture-args.json")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("cancelled ensure executed helper")
	}
}
func TestProviderHostMalformedStatusCannotLaunchOnOccupiedEndpoint(t *testing.T) {
	for _, body := range []string{"not json", "", `{"protocol_version":"1"}`, `[]`, strings.Repeat("x", 1<<20)} {
		t.Run(fmt.Sprintf("body-length-%d", len(body)), func(t *testing.T) {
			c := processClient(t, "")
			listener, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", c.state.Port))
			if err != nil {
				t.Fatal(err)
			}
			server := &http.Server{ReadHeaderTimeout: time.Second, Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(body)) })}
			go func() { _ = server.Serve(listener) }()
			t.Cleanup(func() { _ = server.Close() })
			before := readManifest(t, c)
			if err = c.Ensure(context.Background()); err == nil {
				t.Fatal("malformed endpoint was adopted")
			}
			if c.state.PID != 0 || readManifest(t, c) != before {
				t.Fatal("malformed live endpoint caused a helper launch")
			}
			if _, err = os.Stat(filepath.Join(c.root, "fixture-args.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("malformed endpoint was replaced")
			}
		})
	}
}
