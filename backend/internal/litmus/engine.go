package litmus

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	maxSocketPath = 103       // agent-browser's Unix preflight: the macOS sun_path limit
	outputCap     = 256 << 10 // per stream; the engine also caps itself via AGENT_BROWSER_MAX_OUTPUT
)

// Tests shorten these or point the socket dir elsewhere.
var (
	cmdTimeout = 30 * time.Second
	socketBase = "/tmp" // not os.TempDir(): macOS temp paths would blow the socket path limit
)

var errTabDrift = errors.New("the app's tab was re-selected after a page it opened took focus, which clears element refs; take a new snapshot and use its refs")

// envAllow is the part of the daemon's environment an engine process may see (mirrors AO's allowlist).
var envAllow = []string{"path", "lang", "lc_all", "lc_ctype", "term", "colorterm", "no_color", "force_color"}

// forbiddenFlags are managed by Litmus, never by the worker (mirrors AO's FORBIDDEN_FLAGS).
var forbiddenFlags = []string{"--cdp", "--auto-connect", "--session", "--namespace", "--profile", "--state", "--restore",
	"--executable-path", "--extension", "--init-script", "--args", "--headers", "--proxy", "--plugin", "--allowed-domains"}

// EngineInfo is what run.json keeps about a leg's engine.
type EngineInfo struct {
	Binary    string `json:"binary,omitempty"`
	RunDir    string `json:"runDir"`
	Session   string `json:"session"`
	Namespace string `json:"namespace"`
	Endpoint  string `json:"endpoint"` // the target's remote-debugging URL
	SocketDir string `json:"socketDir"`
}

// execFn runs one engine client process to completion and returns its stdout.
type execFn func(ctx context.Context, bin string, args, env []string) (string, error)

// Engine drives one launched leg through the bundled agent-browser. Every command is
// its own client process; the engine's per-session daemon lives on between them.
type Engine struct {
	EngineInfo
	exec        execFn
	connectPage func(context.Context) (*pageCDP, error) // nil uses the leg's CDP endpoint
	mu          sync.Mutex                              // one action at a time per leg; Drive holds it across a whole receipt
	last        string                                  // the previous after-shot of this leg
	theme       string                                  // Litmus's current prefers-color-scheme override; empty follows the system
	refs        map[string]refInfo                      // the last snapshot's elements by @ref, so receipts can name what was acted on
	once        sync.Once                               // home dir, config.json, the stream-off call and the app tab lookup
	tab         string                                  // the app's own tab: the engine's active tab follows pages the app opens, so it is re-selected when it moves
	ierr        error
	life        context.Context // canceled by Close, which stops anything in flight
	stop        context.CancelFunc
}

func newEngine(info EngineInfo, run execFn) *Engine {
	life, stop := context.WithCancel(context.Background())
	return &Engine{EngineInfo: info, exec: run, life: life, stop: stop}
}

// NewEngine allocates the leg's engine: a short /tmp socket dir (the default temp dir
// is too long for a Unix socket on macOS) and a namespace of its own.
func NewEngine(binary, runDir, leg string, debugPort int, run execFn) (*Engine, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return nil, err
	}
	id := hex.EncodeToString(b[:])
	info := EngineInfo{Binary: binary, RunDir: runDir, Session: leg + "-" + id[:6], Namespace: leg + "-" + id[:6],
		Endpoint: "http://127.0.0.1:" + strconv.Itoa(debugPort), SocketDir: filepath.Join(socketBase, "litmus-"+id)}
	if n := len(socketPath(info.SocketDir, info.Namespace)); n > maxSocketPath {
		return nil, fmt.Errorf("engine socket path is %d bytes, the limit is %d", n, maxSocketPath)
	}
	if err := os.Mkdir(info.SocketDir, 0o700); err != nil {
		return nil, err
	}
	return newEngine(info, run), nil
}

func socketPath(dir, ns string) string {
	return filepath.Join(dir, "namespaces", ns, "run", ns+".sock")
}

func (e *Engine) home() string { return filepath.Join(e.RunDir, "engine", e.Namespace) }

func (e *Engine) env() []string {
	var env []string
	for _, kv := range os.Environ() {
		if k, _, _ := strings.Cut(kv, "="); slices.Contains(envAllow, strings.ToLower(k)) {
			env = append(env, kv)
		}
	}
	h := e.home()
	return append(env, "HOME="+h, "USERPROFILE="+h, "XDG_CONFIG_HOME="+h, "XDG_CACHE_HOME="+h, "TEMP="+h, "TMP="+h, "TMPDIR="+h,
		"AGENT_BROWSER_CONFIG="+filepath.Join(h, "config.json"), "AGENT_BROWSER_CDP="+e.Endpoint,
		"AGENT_BROWSER_SOCKET_DIR="+e.SocketDir, "AGENT_BROWSER_SESSION="+e.Session, "AGENT_BROWSER_NAMESPACE="+e.Namespace,
		"AGENT_BROWSER_CONTENT_BOUNDARIES=1", "AGENT_BROWSER_MAX_OUTPUT=50000", "AGENT_BROWSER_IDLE_TIMEOUT_MS=300000", "AGENT_BROWSER_AUTO_CONNECT=0")
}

func (e *Engine) invoke(ctx context.Context, args ...string) (string, error) {
	return e.exec(ctx, e.Binary, append([]string{"--session", e.Session}, args...), e.env())
}

// Run executes one engine command with a timeout; the process is killed when it runs out.
func (e *Engine) Run(ctx context.Context, timeout time.Duration, args ...string) (string, error) {
	if e.Binary == "" {
		return "", apierr.Unavailable("TESTING_ENGINE_MISSING", "agent-browser binary not found: set AO_AGENT_BROWSER_PATH, or place it at ../agent-browser/agent-browser beside the daemon")
	}
	e.once.Do(func() {
		if e.ierr = os.MkdirAll(e.home(), 0o700); e.ierr == nil {
			e.ierr = os.WriteFile(filepath.Join(e.home(), "config.json"), []byte("{}\n"), 0o600)
		}
		if e.ierr == nil { // a fresh engine daemon streams by default: close that input surface, as AO does
			sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			_, _ = e.invoke(sctx, "stream", "disable")
			if list, err := e.invoke(sctx, "tab", "list"); err == nil {
				_, e.tab = parseTabs(list)
			}
			cancel()
		}
	})
	if e.ierr != nil {
		return "", e.ierr
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	defer context.AfterFunc(e.life, cancel)()
	if e.tab != "" && args[0] != "tab" { // opening a session adds a hidden about:blank page and the active tab moves to it; selecting a tab drops refs, so only when it moved
		if list, err := e.invoke(ctx, "tab", "list"); err == nil {
			if active, _ := parseTabs(list); active != e.tab {
				_, _ = e.invoke(ctx, "tab", e.tab)
				if slices.ContainsFunc(args, func(a string) bool { return strings.HasPrefix(a, "@e") }) {
					return "", errTabDrift
				}
			}
		}
	}
	out, err := e.invoke(ctx, args...)
	if err != nil {
		err = fmt.Errorf("agent-browser %s: %w", args[0], err)
	}
	return out, err
}

var tabLine = regexp.MustCompile(`^(→)? *\[(t\d+)\] .* - (\S+)\s*$`)

// parseTabs reads `tab list` ("→ [t1] title - url", the arrow on the active tab): the active tab's id
// and the first real (not about:) page's id.
func parseTabs(list string) (active, app string) {
	for _, l := range strings.Split(list, "\n") {
		if m := tabLine.FindStringSubmatch(l); m != nil {
			if m[1] != "" {
				active = m[2]
			}
			if app == "" && !strings.HasPrefix(m[3], "about:") {
				app = m[2]
			}
		}
	}
	return active, app
}

// Close stops anything in flight, closes the engine session and kills a daemon that
// outlived it (found through the pid file in our own socket dir), then removes the socket dir.
// An engine that never ran a command has no daemon, so nothing is spawned just to close it.
func (e *Engine) Close(sys sysOps) {
	e.stop()
	if e.SocketDir == "" || !strings.HasPrefix(filepath.Base(e.SocketDir), "litmus-") {
		return
	}
	pids, _ := filepath.Glob(filepath.Join(e.SocketDir, "namespaces", "*", "run", "*.pid"))
	if len(pids) > 0 && e.Binary != "" {
		cctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, _ = e.invoke(cctx, "close")
		cancel()
		procs := sys.procs()
		for _, f := range pids {
			data, _ := os.ReadFile(f)
			if pid, err := strconv.Atoi(strings.TrimSpace(string(data))); err == nil && pid > 0 && strings.Contains(procs[pid], filepath.Base(e.Binary)) {
				sys.kill(pid)
			}
		}
	}
	_ = os.RemoveAll(e.SocketDir)
}

// capWriter keeps the first max bytes and discards the rest without failing the process.
type capWriter struct {
	buf []byte
	max int
}

func (w *capWriter) Write(p []byte) (int, error) {
	if room := w.max - len(w.buf); room > 0 {
		w.buf = append(w.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}

func realExec(ctx context.Context, bin string, args, env []string) (string, error) {
	cmd := process.CommandContext(ctx, bin, args...)
	out, errOut := &capWriter{max: outputCap}, &capWriter{max: outputCap}
	cmd.Env, cmd.Stdout, cmd.Stderr, cmd.WaitDelay = env, out, errOut, 2*time.Second // WaitDelay: a daemon holding our pipes cannot stall the kill
	err := cmd.Run()
	if cerr := ctx.Err(); cerr != nil {
		return string(out.buf), fmt.Errorf("killed: %w", cerr)
	}
	if err != nil {
		msg := strings.TrimSpace(string(errOut.buf))
		if msg == "" {
			msg = strings.TrimSpace(string(out.buf))
		}
		return string(out.buf), errors.New(err.Error() + ": " + lastLines([]byte(msg), 10))
	}
	return string(out.buf), nil
}

// resolveBinary finds agent-browser: AO_AGENT_BROWSER_PATH, else <daemon dir>/../agent-browser/agent-browser.
func resolveBinary(override, exe string) (string, error) {
	path := strings.TrimSpace(override)
	if path == "" {
		path = filepath.Join(filepath.Dir(exe), "..", "agent-browser", "agent-browser")
	}
	path, _ = filepath.Abs(path)
	if st, err := os.Stat(path); err != nil || st.IsDir() {
		return "", fmt.Errorf("agent-browser not found at %s", path)
	}
	return path, nil
}
