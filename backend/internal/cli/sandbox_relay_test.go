package cli

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestSandboxRelayPolicyCoversEveryRootCommand(t *testing.T) {
	root := NewRootCommand(Deps{})
	for _, c := range root.Commands() {
		name := c.Name()
		if name == "help" {
			continue
		}
		if _, ok := sandboxRelayPolicy[name]; !ok {
			t.Errorf("root command %q has no sandbox relay decision; add it to sandboxRelayPolicy", name)
		}
	}
	for _, denied := range []string{"launch", "agent-process", "pty-host", "project", "daemon", "spawn", "send", "cue", "automation", "render", "review", "browser", "session", "orchestrator", "pr"} {
		if sandboxRelayPolicy[denied] {
			t.Errorf("%q must be denied: it can execute host commands or disable the sandbox", denied)
		}
	}
}

func writeRelayRequest(t *testing.T, dir, id string, argv []string, stdin string) {
	t.Helper()
	tmp := filepath.Join(dir, "req", id+".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	var raw strings.Builder
	for _, a := range argv {
		raw.WriteString(a + "\x00")
	}
	_ = os.WriteFile(filepath.Join(tmp, "argv"), []byte(raw.String()), 0o600)
	_ = os.WriteFile(filepath.Join(tmp, "stdin"), []byte(stdin), 0o600)
	if err := os.Rename(tmp, filepath.Join(dir, "req", id)); err != nil {
		t.Fatal(err)
	}
}

func waitRelayResponse(t *testing.T, dir, id string) (string, string, string) {
	t.Helper()
	resp := filepath.Join(dir, "resp", id)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(resp); err == nil {
			read := func(n string) string { b, _ := os.ReadFile(filepath.Join(resp, n)); return string(b) }
			return read("stdout"), read("stderr"), read("exit")
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no relay response for %s", id)
	return "", "", ""
}

func newRelayDir(t *testing.T) string {
	dir := t.TempDir()
	for _, d := range []string{"req", "work", "resp"} {
		_ = os.MkdirAll(filepath.Join(dir, d), 0o700)
	}
	return dir
}

type relayCall struct {
	argv  []string
	stdin string
}

// startRelay serves dir with a runner that records calls on a buffered channel.
func startRelay(t *testing.T, dir string) chan relayCall {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	calls := make(chan relayCall, 16)
	go func() {
		defer close(done)
		serveSandboxRelay(ctx, dir, func(_ context.Context, argv []string, stdin []byte) ([]byte, []byte, int) {
			calls <- relayCall{argv, string(stdin)}
			return []byte("ok"), []byte("warn"), 0
		})
	}()
	t.Cleanup(func() { cancel(); <-done })
	return calls
}

func TestSandboxRelayRunsAllowedCommand(t *testing.T) {
	dir := newRelayDir(t)
	calls := startRelay(t, dir)
	writeRelayRequest(t, dir, "1-2-3", []string{"hooks", "claude-code", "stop"}, `{"e":1}`)
	out, errOut, code := waitRelayResponse(t, dir, "1-2-3")
	if out != "ok" || errOut != "warn" || code != "0" {
		t.Fatalf("resp = %q %q %q", out, errOut, code)
	}
	got := <-calls
	if strings.Join(got.argv, " ") != "hooks claude-code stop" || got.stdin != `{"e":1}` {
		t.Fatalf("ran %q with stdin %q", got.argv, got.stdin)
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		entries, _ := os.ReadDir(filepath.Join(dir, "work"))
		if len(entries) == 0 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("work dir not cleaned: %v", entries)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func requireRefused(t *testing.T, dir, id, wantErr string) {
	t.Helper()
	_, errOut, code := waitRelayResponse(t, dir, id)
	if code != "126" || !strings.Contains(errOut, wantErr) {
		t.Fatalf("id %s: code %q stderr %q, want 126 containing %q", id, code, errOut, wantErr)
	}
}

func requireNoCall(t *testing.T, calls chan relayCall) {
	t.Helper()
	select {
	case c := <-calls:
		t.Fatalf("refused request reached the host runner: %q", c.argv)
	default:
	}
}

func TestSandboxRelayRefusesDeniedCommand(t *testing.T) {
	dir := newRelayDir(t)
	calls := startRelay(t, dir)
	for i, argv := range [][]string{
		{"agent-process", "supervise", "--", "sh"},
		{"--data-dir", "/x", "hooks"}, // leading global flags are refused, not parsed
		{},
		{"spawn", "x"},
		{"send", "s", "hi"},
		{"cue", "list"},
		{"automation", "list"},
		{"render", "/etc/passwd"},
		{"review", "x"},
		{"browser", "x"},
	} {
		id := "9-9-" + strconv.Itoa(i)
		writeRelayRequest(t, dir, id, argv, "")
		requireRefused(t, dir, id, "not allowed in the sandbox")
	}
	requireNoCall(t, calls)
}

func TestSandboxRelayPreviewAllowsOnlyHTTPURL(t *testing.T) {
	for _, argv := range [][]string{
		{"preview", "http://localhost:5173"},
		{"preview", "https://example.com/x"},
	} {
		if !sandboxRelayAllowed(argv) {
			t.Errorf("%q should be allowed", argv)
		}
	}
	for _, argv := range [][]string{
		{"preview"}, {"preview", "README.md"}, {"preview", "/etc/passwd"},
		{"preview", "file:///etc/passwd"}, {"preview", "start"}, {"preview", "clear"},
		{"preview", "--json", "http://x"}, {"preview", "http://x", "y"}, {"preview", "http://"},
	} {
		if sandboxRelayAllowed(argv) {
			t.Errorf("%q should be refused", argv)
		}
	}
	dir := newRelayDir(t)
	calls := startRelay(t, dir)
	writeRelayRequest(t, dir, "4-4-4", []string{"preview", "file:///etc/passwd"}, "")
	requireRefused(t, dir, "4-4-4", "not allowed in the sandbox")
	requireNoCall(t, calls)
}

func TestSandboxRelayDoesNotFollowResponseDirSymlink(t *testing.T) {
	dir := newRelayDir(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(dir, "resp", "5-5-5.tmp")); err != nil {
		t.Fatal(err)
	}
	startRelay(t, dir)
	writeRelayRequest(t, dir, "5-5-5", []string{"hooks", "a", "b"}, "")
	waitRelayResponse(t, dir, "5-5-5")
	if entries, _ := os.ReadDir(outside); len(entries) != 0 {
		t.Fatalf("relay wrote outside the spool dir: %v", entries)
	}
}

func TestSandboxRelayDoesNotFollowResponseFileSymlink(t *testing.T) {
	dir := newRelayDir(t)
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmp := filepath.Join(dir, "resp", "6-6-6.tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(tmp, "stdout")); err != nil {
		t.Fatal(err)
	}
	startRelay(t, dir)
	writeRelayRequest(t, dir, "6-6-6", []string{"hooks", "a", "b"}, "")
	out, _, _ := waitRelayResponse(t, dir, "6-6-6")
	if b, _ := os.ReadFile(outside); string(b) != "untouched" {
		t.Fatalf("outside file modified: %q", b)
	}
	if out != "ok" {
		t.Fatalf("response stdout = %q", out)
	}
}

func TestSandboxRelayRefusesSymlinkedRequestFile(t *testing.T) {
	dir := newRelayDir(t)
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte("topsecret"), 0o600); err != nil {
		t.Fatal(err)
	}
	calls := startRelay(t, dir)
	tmp := filepath.Join(dir, "req", "7-7-7.tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		t.Fatal(err)
	}
	_ = os.WriteFile(filepath.Join(tmp, "argv"), []byte("hooks\x00a\x00b\x00"), 0o600)
	if err := os.Symlink(secret, filepath.Join(tmp, "stdin")); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, filepath.Join(dir, "req", "7-7-7")); err != nil {
		t.Fatal(err)
	}
	requireRefused(t, dir, "7-7-7", "malformed")
	requireNoCall(t, calls)
}

func TestSandboxRelayRefusesOversizedRequest(t *testing.T) {
	dir := newRelayDir(t)
	calls := startRelay(t, dir)
	writeRelayRequest(t, dir, "8-8-8", []string{"hooks", "a", "b"}, strings.Repeat("x", sandboxRelayMaxBytes+1))
	requireRefused(t, dir, "8-8-8", "ao: request too large for the sandbox relay")
	requireNoCall(t, calls)
}

func TestSandboxRelayCapsInFlightHandlers(t *testing.T) {
	dir := newRelayDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	release := make(chan struct{})
	var mu sync.Mutex
	cur, peak := 0, 0
	go func() {
		defer close(done)
		serveSandboxRelay(ctx, dir, func(context.Context, []string, []byte) ([]byte, []byte, int) {
			mu.Lock()
			cur++
			if cur > peak {
				peak = cur
			}
			mu.Unlock()
			<-release
			mu.Lock()
			cur--
			mu.Unlock()
			return nil, nil, 0
		})
	}()
	defer func() { cancel(); <-done }()
	const total = sandboxRelayMaxInFlight + 3
	for i := 0; i < total; i++ {
		writeRelayRequest(t, dir, "3-3-"+strconv.Itoa(i), []string{"hooks", "a", "b"}, "")
	}
	time.Sleep(500 * time.Millisecond)
	if entries, _ := os.ReadDir(filepath.Join(dir, "req")); len(entries) != total-sandboxRelayMaxInFlight {
		t.Fatalf("unclaimed requests = %d, want %d", len(entries), total-sandboxRelayMaxInFlight)
	}
	close(release)
	for i := 0; i < total; i++ {
		waitRelayResponse(t, dir, "3-3-"+strconv.Itoa(i))
	}
	mu.Lock()
	defer mu.Unlock()
	if peak > sandboxRelayMaxInFlight {
		t.Fatalf("peak in-flight = %d, want <= %d", peak, sandboxRelayMaxInFlight)
	}
}

func TestSandboxRelayMissingDirsDoNotCrash(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	serveSandboxRelay(ctx, t.TempDir(), nil) // returns when ctx ends
	serveSandboxRelay(ctx, filepath.Join(t.TempDir(), "absent"), nil)
}

func TestCappedBufferTruncates(t *testing.T) {
	c := &cappedBuffer{max: 4}
	if n, _ := c.Write([]byte("abcdef")); n != 6 {
		t.Fatalf("n = %d", n)
	}
	_, _ = c.Write([]byte("gh"))
	if c.buf.String() != "abcd" || !c.truncated {
		t.Fatalf("buf = %q truncated = %v", c.buf.String(), c.truncated)
	}
}

// C1c: the container can replace its session's relay dir with a symlink to
// any host dir. The relay must refuse it rather than serve (and write
// responses into) the link target.
func TestSandboxRelayRefusesSymlinkedRelayDir(t *testing.T) {
	session := t.TempDir()
	outside := newRelayDir(t)
	if err := os.Symlink(outside, filepath.Join(session, "relay")); err != nil {
		t.Fatal(err)
	}
	writeRelayRequest(t, outside, "9-9-9", []string{"hooks", "a", "b"}, "")
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	calls := make(chan struct{}, 1)
	serveSandboxRelay(ctx, filepath.Join(session, "relay"), func(context.Context, []string, []byte) ([]byte, []byte, int) {
		calls <- struct{}{}
		return nil, nil, 0
	})
	select {
	case <-calls:
		t.Fatal("relay served a request through a symlinked relay dir")
	default:
	}
	if _, err := os.Stat(filepath.Join(outside, "resp", "9-9-9")); err == nil {
		t.Fatal("relay wrote a response through a symlinked relay dir")
	}
}

func TestSandboxRelayReportsUnopenableRelayDir(t *testing.T) {
	session := t.TempDir()
	if err := os.Symlink(t.TempDir(), filepath.Join(session, "relay")); err != nil {
		t.Fatal(err)
	}
	if err := serveSandboxRelay(context.Background(), filepath.Join(session, "relay"), nil); err == nil {
		t.Fatal("serveSandboxRelay = nil, want an error for a symlinked relay dir")
	}
}
