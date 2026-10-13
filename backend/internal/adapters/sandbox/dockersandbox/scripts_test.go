package dockersandbox

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestRelayClientScriptRoundTrip(t *testing.T) {
	bin := t.TempDir()
	if err := InstallBin(bin); err != nil {
		t.Fatal(err)
	}
	relay := t.TempDir()
	for _, d := range []string{"req", "resp"} {
		if err := os.MkdirAll(filepath.Join(relay, d), 0o700); err != nil {
			t.Fatal(err)
		}
	}
	done := make(chan []string, 1)
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
				done <- args
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		done <- nil
	}()
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
	e, ok := err.(*exec.ExitError)
	*target = e
	return ok
}
