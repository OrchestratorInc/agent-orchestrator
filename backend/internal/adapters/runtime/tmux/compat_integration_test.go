//go:build !windows

package tmux

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/runtime/ptyexec"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestRealTmuxCrossVersionAttach runs the production Runtime against two real
// tmux releases sharing one private server: a client that can run commands
// against an older server but cannot attach to it (3.7b against 3.5a reports
// "open terminal failed: not a terminal"). It needs both binaries, so it runs
// only when AO_TMUX_COMPAT_OLD and AO_TMUX_COMPAT_NEW name tmux executables
// whose `tmux -V` differ, for example AO's bundled 3.5a and a 3.7b build.
func TestRealTmuxCrossVersionAttach(t *testing.T) {
	oldBinary, newBinary := os.Getenv("AO_TMUX_COMPAT_OLD"), os.Getenv("AO_TMUX_COMPAT_NEW")
	if oldBinary == "" || newBinary == "" {
		t.Skip("set AO_TMUX_COMPAT_OLD and AO_TMUX_COMPAT_NEW to two tmux releases")
	}
	oldVersion, newVersion := realTmuxVersion(t, oldBinary), realTmuxVersion(t, newBinary)
	if oldVersion == newVersion {
		t.Skipf("both binaries report tmux %s; the test needs two releases", oldVersion)
	}
	t.Logf("server/old client tmux %s, current/new client tmux %s", oldVersion, newVersion)

	t.Run("same-path re-stage keeps the retained client until its server is gone", func(t *testing.T) {
		env := newCompatEnv(t)
		staged := env.install(t, newBinary, "0.13.0", "tmux")
		retained := retainedClientPath(staged, env.socket)
		copyExecutable(t, oldBinary, retained)
		env.startServer(t, retained, "sess-old", "AO_XV_OLD_SERVER")
		env.controlAttach(t, staged, "sess-old")

		store := &fakeCompatibleClientStore{}
		r := env.runtime(staged, store)
		handle := ports.RuntimeHandle{ID: "sess-old"}
		for probe := 1; probe <= 2; probe++ {
			if alive, err := r.IsAlive(context.Background(), handle); err != nil || !alive {
				t.Fatalf("IsAlive #%d = (%v, %v), want (true, nil)", probe, alive, err)
			}
		}
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("retained tmux %s client after IsAlive: %v, want kept", oldVersion, err)
		}
		if got := r.compatibleBinaryForSocket(env.socket); got != retained {
			t.Fatalf("selected client = %q, want retained tmux %s %q", got, oldVersion, retained)
		}
		env.attachAndRead(t, r, handle, "AO_XV_OLD_SERVER")

		// The old server goes away; the next Create starts one with the current
		// client, and only then is the retained copy retired.
		env.tmux(t, retained, "kill-server")
		created, err := r.Create(context.Background(), env.config("sess-new", "AO_XV_NEW_SERVER"))
		if err != nil {
			t.Fatalf("Create after old server exit: %v", err)
		}
		if got := env.serverVersion(t, staged); got != newVersion {
			t.Fatalf("server after Create runs tmux %s, want %s", got, newVersion)
		}
		if _, err := os.Stat(retained); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retained client after its server exited: stat err %v, want removed", err)
		}
		if alive, err := r.IsAlive(context.Background(), created); err != nil || !alive {
			t.Fatalf("IsAlive(new) = (%v, %v), want (true, nil)", alive, err)
		}
		if got := r.compatibleBinaryForSocket(env.socket); got != staged || store.path != staged {
			t.Fatalf("selected = %q, association = %+v, want current client %q", got, store, staged)
		}
		env.attachAndRead(t, r, created, "AO_XV_NEW_SERVER")
	})

	t.Run("older staged client creates sessions through the retained client", func(t *testing.T) {
		// The reviewer's Create case: the surviving server is the newer release
		// and the staged client the older one, which the server rejects with
		// "server exited unexpectedly". new-session is never replayed after that,
		// so it has to go through the retained client from the start.
		env := newCompatEnv(t)
		staged := env.install(t, oldBinary, "0.13.0", "tmux")
		retained := retainedClientPath(staged, env.socket)
		copyExecutable(t, newBinary, retained)
		env.startServer(t, retained, "sess-anchor", "AO_XV_ANCHOR")

		store := &fakeCompatibleClientStore{}
		r := env.runtime(staged, store)
		if alive, err := r.IsAlive(context.Background(), ports.RuntimeHandle{ID: "sess-anchor"}); err != nil || !alive {
			t.Fatalf("IsAlive = (%v, %v), want (true, nil)", alive, err)
		}
		if got := r.compatibleBinaryForSocket(env.socket); got != retained {
			t.Fatalf("selected client = %q, want retained tmux %s %q", got, newVersion, retained)
		}
		for i := 1; i <= 3; i++ {
			id := "sess-created-" + strconv.Itoa(i)
			marker := "AO_XV_CREATED_" + strconv.Itoa(i)
			handle, err := r.Create(context.Background(), env.config(id, marker))
			if err != nil {
				t.Fatalf("Create #%d with retained client selected: %v", i, err)
			}
			env.attachAndRead(t, r, handle, marker)
		}
		if got := env.serverVersion(t, retained); got != newVersion {
			t.Fatalf("server runs tmux %s, want the surviving %s server", got, newVersion)
		}
		if _, err := os.Stat(retained); err != nil {
			t.Fatalf("retained client: %v, want kept while its server lives", err)
		}
	})

	t.Run("app-version upgrade attaches through the server's own binary", func(t *testing.T) {
		env := newCompatEnv(t)
		previous := env.install(t, oldBinary, "0.13.0", "tmux")
		staged := env.install(t, newBinary, "0.14.0", "tmux")
		env.startServer(t, previous, "sess-upgrade", "AO_XV_UPGRADE")
		env.controlAttach(t, staged, "sess-upgrade")

		store := &fakeCompatibleClientStore{}
		r := env.runtime(staged, store)
		handle := ports.RuntimeHandle{ID: "sess-upgrade"}
		if alive, err := r.IsAlive(context.Background(), handle); err != nil || !alive {
			t.Fatalf("IsAlive = (%v, %v), want (true, nil)", alive, err)
		}
		env.attachAndRead(t, r, handle, "AO_XV_UPGRADE")
		if got := r.compatibleBinaryForSocket(env.socket); !sameExecutable(got, previous) {
			t.Fatalf("selected client = %q, want previous version's client %q", got, previous)
		}
		if store.managed {
			t.Fatalf("association = %+v, want the previous version's binary unmanaged", store)
		}
	})
}

func realTmuxVersion(t *testing.T, binary string) string {
	t.Helper()
	out, err := exec.Command(binary, "-V").CombinedOutput()
	if err != nil {
		t.Fatalf("%s -V: %v: %s", binary, err, out)
	}
	version, ok := strings.CutPrefix(strings.TrimSpace(string(out)), "tmux ")
	if !ok {
		t.Fatalf("%s -V = %q, want tmux <version>", binary, out)
	}
	return version
}

func copyExecutable(t *testing.T, source, destination string) {
	t.Helper()
	data, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Dir(destination), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(destination, data, 0o755); err != nil {
		t.Fatal(err)
	}
}

func sameExecutable(a, b string) bool {
	ai, errA := os.Stat(a)
	bi, errB := os.Stat(b)
	return errA == nil && errB == nil && os.SameFile(ai, bi)
}

type compatEnv struct {
	root   string
	socket string
}

func newCompatEnv(t *testing.T) *compatEnv {
	t.Helper()
	// A short socket root stays below macOS's Unix socket path limit.
	socketRoot, err := os.MkdirTemp("/tmp", "ao-xv-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(socketRoot) })
	t.Setenv("TMUX_TMPDIR", socketRoot)
	t.Setenv("TMUX", "")
	t.Setenv("AO_TMUX_SOCKET_NAME", "")
	t.Setenv("AO_TMUX_BINARY", "")
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SHELL", "/bin/sh")
	t.Setenv("ENV", "")
	t.Setenv("TERM", "xterm-256color")
	env := &compatEnv{root: t.TempDir(), socket: "ao-xv-" + strconv.Itoa(os.Getpid())}
	t.Cleanup(func() {
		for _, binary := range env.installed() {
			_ = exec.Command(binary, "-L", env.socket, "kill-server").Run()
		}
	})
	return env
}

func (e *compatEnv) installed() []string {
	matches, _ := filepath.Glob(filepath.Join(e.root, "*", "tmux*"))
	return matches
}

// install copies binary to <root>/<appVersion>/<name>, mirroring the staged
// layout ~/.ao/runtime/tmux/<appVersion>-<platform>-<arch>/tmux.
func (e *compatEnv) install(t *testing.T, binary, appVersion, name string) string {
	t.Helper()
	destination := filepath.Join(e.root, appVersion, name)
	copyExecutable(t, binary, destination)
	return destination
}

func (e *compatEnv) tmux(t *testing.T, binary string, args ...string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	args = append([]string{"-L", e.socket, "-f", "/dev/null"}, args...)
	out, err := exec.CommandContext(ctx, binary, args...).CombinedOutput()
	if err != nil {
		t.Fatalf("%s %v: %v: %s", binary, args, err, out)
	}
	return string(out)
}

func (e *compatEnv) startServer(t *testing.T, binary, session, marker string) {
	t.Helper()
	e.tmux(t, binary, "new-session", "-d", "-s", session, "-x", "80", "-y", "24",
		"/bin/sh", "-c", "echo "+marker+"; exec /bin/sh")
}

func (e *compatEnv) serverVersion(t *testing.T, client string) string {
	t.Helper()
	return strings.TrimSpace(e.tmux(t, client, "display-message", "-p", "#{version}"))
}

func (e *compatEnv) runtime(binary string, store CompatibleClientStore) *Runtime {
	return New(Options{
		Binary: binary, LegacyBinary: binary, SocketName: e.socket, Shell: "/bin/sh",
		Timeout: 10 * time.Second, CompatibleClientStore: store,
	})
}

func (e *compatEnv) config(id, marker string) ports.RuntimeConfig {
	return ports.RuntimeConfig{
		SessionID:     domain.SessionID(id),
		WorkspacePath: e.root,
		Argv:          []string{"/bin/sh", "-c", "echo " + marker},
	}
}

// controlAttach records whether the current client alone can attach to the
// older server, the behavior the fix works around. It does not fail the test:
// the pair under test decides it.
func (e *compatEnv) controlAttach(t *testing.T, client, session string) {
	t.Helper()
	argv := []string{client, "-L", e.socket, "-u", "-T", "RGB", "attach-session", "-t", "=" + session}
	out, err := readAttach(argv, "", 5*time.Second)
	t.Logf("control: current client attach to the older server: err=%v output=%q", err, lastLine(out))
}

func (e *compatEnv) attachAndRead(t *testing.T, r *Runtime, handle ports.RuntimeHandle, marker string) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := r.Attach(ctx, handle, 24, 80)
	if err != nil {
		t.Fatalf("Attach(%s): %v", handle.ID, err)
	}
	out, err := readStream(stream, marker, 10*time.Second)
	_ = stream.Close()
	if err != nil {
		t.Fatalf("Attach(%s) read: %v; output %q", handle.ID, err, lastLine(out))
	}
	if strings.Contains(out, "open terminal failed") {
		t.Fatalf("Attach(%s) output %q, want the pane", handle.ID, lastLine(out))
	}
}

func readAttach(argv []string, marker string, deadline time.Duration) (string, error) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := ptyexec.Spawn(ctx, argv, attachEnv(os.Environ()), 24, 80)
	if err != nil {
		return "", err
	}
	defer func() { _ = stream.Close() }()
	return readStream(stream, marker, deadline)
}

// readStream reads until marker appears (or, with no marker, until the stream
// ends or deadline passes).
func readStream(stream io.Reader, marker string, deadline time.Duration) (string, error) {
	var (
		mu  sync.Mutex
		buf bytes.Buffer
	)
	done := make(chan error, 1)
	go func() {
		chunk := make([]byte, 4096)
		for {
			n, err := stream.Read(chunk)
			mu.Lock()
			buf.Write(chunk[:n])
			found := marker != "" && strings.Contains(buf.String(), marker)
			mu.Unlock()
			if found {
				done <- nil
				return
			}
			if err != nil {
				done <- err
				return
			}
		}
	}()
	var err error
	select {
	case err = <-done:
	case <-time.After(deadline):
		err = errors.New("timed out")
	}
	mu.Lock()
	defer mu.Unlock()
	return buf.String(), err
}

func lastLine(out string) string {
	out = strings.TrimSpace(strings.ReplaceAll(out, "\r", "\n"))
	if i := strings.LastIndex(out, "\n"); i >= 0 {
		out = out[i+1:]
	}
	if len(out) > 200 {
		out = out[len(out)-200:]
	}
	return out
}
