package cli

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	for _, denied := range []string{"launch", "agent-process", "pty-host", "project", "daemon"} {
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

func TestSandboxRelayRunsAllowedCommand(t *testing.T) {
	dir := newRelayDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var gotArgv []string
	var gotStdin string
	go serveSandboxRelay(ctx, dir, func(_ context.Context, argv []string, stdin []byte) ([]byte, []byte, int) {
		gotArgv, gotStdin = argv, string(stdin)
		return []byte("ok"), []byte("warn"), 0
	})
	writeRelayRequest(t, dir, "1-2-3", []string{"hooks", "claude-code", "stop"}, `{"e":1}`)
	out, errOut, code := waitRelayResponse(t, dir, "1-2-3")
	if out != "ok" || errOut != "warn" || code != "0" {
		t.Fatalf("resp = %q %q %q", out, errOut, code)
	}
	if strings.Join(gotArgv, " ") != "hooks claude-code stop" || gotStdin != `{"e":1}` {
		t.Fatalf("ran %q with stdin %q", gotArgv, gotStdin)
	}
	if entries, _ := os.ReadDir(filepath.Join(dir, "work")); len(entries) != 0 {
		t.Fatalf("work dir not cleaned: %v", entries)
	}
}

func TestSandboxRelayRefusesDeniedCommand(t *testing.T) {
	dir := newRelayDir(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ran := false
	go serveSandboxRelay(ctx, dir, func(context.Context, []string, []byte) ([]byte, []byte, int) {
		ran = true
		return nil, nil, 0
	})
	for i, argv := range [][]string{
		{"agent-process", "supervise", "--", "sh"},
		{"--data-dir", "/x", "hooks"}, // leading global flags are refused, not parsed
		{},
	} {
		id := "9-9-" + string(rune('0'+i))
		writeRelayRequest(t, dir, id, argv, "")
		_, errOut, code := waitRelayResponse(t, dir, id)
		if code != "126" || !strings.Contains(errOut, "not allowed in the sandbox") {
			t.Fatalf("argv %q: code %q stderr %q", argv, code, errOut)
		}
	}
	if ran {
		t.Fatal("denied command reached the host runner")
	}
}
