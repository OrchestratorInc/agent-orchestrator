package dockersandbox

import (
	"bytes"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestInstallBinConcurrent(t *testing.T) {
	bin := t.TempDir()
	const n = 8
	errs := make(chan error, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- InstallBin(bin)
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"ao", "ao-enter"} {
		info, err := os.Stat(filepath.Join(bin, name))
		if err != nil {
			t.Fatal(err)
		}
		if info.Mode().Perm() != 0o700 {
			t.Fatalf("%s mode = %v, want 0700", name, info.Mode().Perm())
		}
	}
	entries, err := os.ReadDir(bin)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("bin dir entries = %v, want only ao and ao-enter", names)
	}
}

// newRelayFixture installs the client scripts and a relay spool, and answers
// the first request like the host: stdout echoes the request's stdin after
// "out:", stderr is "err" and the exit code is 3. The channel yields the argv.
func newRelayFixture(t *testing.T) (bin, relay string, done <-chan []string) {
	t.Helper()
	bin = t.TempDir()
	if err := InstallBin(bin); err != nil {
		t.Fatal(err)
	}
	relay = t.TempDir()
	for _, d := range []string{"req", "resp"} {
		if err := os.MkdirAll(filepath.Join(relay, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	ch := make(chan []string, 1)
	go func() { // fake host: answer the first request
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			entries, _ := os.ReadDir(filepath.Join(relay, "req"))
			for _, e := range entries {
				if strings.HasSuffix(e.Name(), ".tmp") {
					continue
				}
				req := filepath.Join(relay, "req", e.Name())
				raw, _ := os.ReadFile(filepath.Join(req, "argv"))
				stdin, _ := os.ReadFile(filepath.Join(req, "stdin"))
				args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
				tmp := filepath.Join(relay, "resp", e.Name()+".tmp")
				_ = os.MkdirAll(tmp, 0o700)
				_ = os.WriteFile(filepath.Join(tmp, "stdout"), append([]byte("out:"), stdin...), 0o600)
				_ = os.WriteFile(filepath.Join(tmp, "stderr"), []byte("err"), 0o600)
				_ = os.WriteFile(filepath.Join(tmp, "exit"), []byte("3"), 0o600)
				_ = os.Rename(tmp, filepath.Join(relay, "resp", e.Name()))
				ch <- args
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		ch <- nil
	}()
	return bin, relay, ch
}

func TestRelayClientScriptRoundTrip(t *testing.T) {
	bin, relay, done := newRelayFixture(t)
	cmd := exec.Command("/bin/sh", filepath.Join(bin, "ao"), "hooks", "claude-code", "arg with space")
	cmd.Env = append(os.Environ(), "AO_SANDBOX_RELAY="+relay)
	cmd.Stdin = strings.NewReader(`{"hook":"x"}`)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if err == nil || !errorsAs(err, &exitErr) || exitErr.ExitCode() != 3 {
		t.Fatalf("exit = %v, want code 3", err)
	}
	if got := <-done; strings.Join(got, "|") != "hooks|claude-code|arg with space" {
		t.Fatalf("argv = %q", got)
	}
	if stdout.String() != `out:{"hook":"x"}` || stderr.String() != "err" {
		t.Fatalf("stdout=%q stderr=%q", stdout.String(), stderr.String())
	}
	if entries, _ := os.ReadDir(filepath.Join(relay, "resp")); len(entries) != 0 {
		t.Fatalf("client left response behind: %v", entries)
	}
}

// TestRelayClientIgnoresOpenStdinForNonHookCommands pins that only hooks read
// stdin: an agent's tool runner may hand `ao status` a pipe it never closes,
// and reading it would hang the client before the request is even sent.
func TestRelayClientIgnoresOpenStdinForNonHookCommands(t *testing.T) {
	bin, relay, done := newRelayFixture(t)
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = pr.Close(); _ = pw.Close() }()
	if _, err := pw.WriteString("not for the host"); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", filepath.Join(bin, "ao"), "status")
	cmd.Env = append(os.Environ(), "AO_SANDBOX_RELAY="+relay)
	cmd.Stdin = pr // the write end stays open: EOF never arrives
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	select {
	case <-exited:
	case <-time.After(10 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("relay client hung reading an open stdin for a non-hook command")
	}
	if got := <-done; strings.Join(got, "|") != "status" {
		t.Fatalf("argv = %q", got)
	}
	if stdout.String() != "out:" {
		t.Fatalf("stdout = %q, want the host to see empty stdin", stdout.String())
	}
}

func TestAoEnterSourcesEnvAndPrependsBin(t *testing.T) {
	bin := t.TempDir()
	if err := InstallBin(bin); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(t.TempDir(), "env")
	if err := os.WriteFile(envFile, []byte("GREETING='hi there'\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("/bin/sh", filepath.Join(bin, "ao-enter"), envFile,
		"/bin/sh", "-c", `printf '%s|%s' "$GREETING" "${PATH%%:*}"`).Output()
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != "hi there|"+bin {
		t.Fatalf("ao-enter output = %q", out)
	}
}

func errorsAs(err error, target **exec.ExitError) bool {
	return errors.As(err, target)
}
