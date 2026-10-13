package cli

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	sandboxRelayPoll        = 50 * time.Millisecond
	sandboxRelayTimeout     = 60 * time.Second
	sandboxRelayMaxBytes    = 8 << 20
	sandboxRelayMaxInFlight = 4
	sandboxRelayWaitDelay   = 5 * time.Second
	sandboxRelayJoinTimeout = 10 * time.Second
)

var sandboxRelayIDPattern = regexp.MustCompile(`^\d+-\d+-\d+$`)

// sandboxRelayPolicy decides which top-level `ao` commands a sandboxed agent
// may run on the host (#6663). Every root command must be listed. The relay
// runs the real host `ao`, so anything that can reach arbitrary host files,
// spawn host processes, mutate other sessions, or reconfigure AO is denied.
// Only the lifecycle callbacks and a URL-only preview are allowed; see
// sandboxRelayAllowed for the argument check on preview.
var sandboxRelayPolicy = map[string]bool{
	"hooks": true, "report": true, "status": true, "version": true, "preview": true,

	"spawn": false, "send": false, "session": false, "orchestrator": false,
	"pr": false, "review": false, "automation": false, "cue": false,
	"render": false, "browser": false,
	"daemon": false, "start": false, "stop": false, "remote-host": false,
	"doctor": false, "agent": false, "mcp": false, "agent-process": false,
	"chat-host": false, "unreal-provider": false, "launch": false,
	"pty-host": false, "codex-login": false, "claude-login": false,
	"import": false, "dev": false, "project": false, "completion": false,
}

// sandboxRelayAllowed applies the policy plus per-command argument checks.
// `ao preview` also accepts workspace file paths and subcommands (start runs a
// dev server from workspace config), so only a single http(s) URL is allowed.
func sandboxRelayAllowed(argv []string) bool {
	if len(argv) == 0 || !sandboxRelayPolicy[argv[0]] {
		return false
	}
	if argv[0] == "preview" {
		if len(argv) != 2 {
			return false
		}
		u, err := url.Parse(argv[1])
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return false
		}
	}
	return true
}

type relayRunFunc func(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int)

// serveSandboxRelay answers relay requests under dir until ctx ends. It
// returns an error at once when dir cannot be opened safely.
func serveSandboxRelay(ctx context.Context, dir string, run relayRunFunc) error {
	root, err := openSandboxRelay(dir)
	if err != nil {
		return err
	}
	serveSandboxRelayRoot(ctx, root, run)
	return nil
}

// openSandboxRelay opens the relay dir through its parent (the session dir).
// The container can replace the relay dir itself with a symlink; opening it
// as a child root refuses a link that leaves the session dir.
func openSandboxRelay(dir string) (*os.Root, error) {
	parent, err := os.OpenRoot(filepath.Dir(dir))
	if err != nil {
		return nil, fmt.Errorf("open sandbox relay: %w", err)
	}
	defer func() { _ = parent.Close() }()
	root, err := parent.OpenRoot(filepath.Base(dir))
	if err != nil {
		return nil, fmt.Errorf("open sandbox relay: %w", err)
	}
	return root, nil
}

// serveSandboxRelayRoot answers relay requests under root until ctx ends and
// closes root. The container controls everything under it, so all access
// goes through the os.Root, which refuses to leave it. It returns after
// in-flight handlers finish.
func serveSandboxRelayRoot(ctx context.Context, root *os.Root, run relayRunFunc) {
	defer func() { _ = root.Close() }() // read-only root; nothing to flush
	sem := make(chan struct{}, sandboxRelayMaxInFlight)
	var wg sync.WaitGroup
	defer wg.Wait()
	ticker := time.NewTicker(sandboxRelayPoll)
	defer ticker.Stop()
	for {
		for _, id := range listRelayRequests(root) {
			select {
			case sem <- struct{}{}:
			default:
				// No free slot: leave the request unclaimed in req/.
				goto wait
			}
			work := "work/" + id
			// Clear anything the container planted at the claim target.
			_ = root.RemoveAll(work)
			// Rename claims the request so it is handled exactly once.
			if err := root.Rename("req/"+id, work); err != nil {
				<-sem
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-sem }()
				handleSandboxRelay(ctx, root, id, work, run)
			}()
		}
	wait:
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func listRelayRequests(root *os.Root) []string {
	d, err := root.Open("req")
	if err != nil {
		return nil
	}
	defer func() { _ = d.Close() }() // read-only dir handle
	entries, _ := d.ReadDir(-1)
	var ids []string
	for _, e := range entries {
		// DirEntry type comes from lstat, so a symlink is not IsDir.
		if e.IsDir() && sandboxRelayIDPattern.MatchString(e.Name()) {
			ids = append(ids, e.Name())
		}
	}
	return ids
}

func handleSandboxRelay(ctx context.Context, root *os.Root, id, work string, run relayRunFunc) {
	defer func() { _ = root.RemoveAll(work) }()
	var stdout, stderr []byte
	code := 126
	argv, tooBigA, errA := readRelayFile(root, work+"/argv")
	stdin, tooBigS, errS := readRelayFile(root, work+"/stdin")
	switch {
	case tooBigA || tooBigS:
		stderr = []byte("ao: request too large for the sandbox relay\n")
	case errA != nil || errS != nil:
		stderr = []byte("ao: malformed sandbox relay request\n")
	default:
		args := splitRelayArgv(argv)
		if !sandboxRelayAllowed(args) {
			name := ""
			if len(args) > 0 {
				name = args[0]
			}
			stderr = []byte(fmt.Sprintf("ao: %q is not allowed in the sandbox\n", name))
		} else {
			rctx, cancel := context.WithTimeout(ctx, sandboxRelayTimeout)
			stdout, stderr, code = run(rctx, args, stdin)
			cancel()
		}
	}
	writeRelayResponse(root, id, stdout, stderr, code)
}

func writeRelayResponse(root *os.Root, id string, stdout, stderr []byte, code int) {
	tmp := "resp/" + id + ".tmp"
	if err := root.Mkdir(tmp, 0o700); err != nil {
		_ = root.RemoveAll(tmp)
		if err := root.Mkdir(tmp, 0o700); err != nil {
			return
		}
	}
	for name, data := range map[string][]byte{
		"stdout": stdout, "stderr": stderr, "exit": []byte(strconv.Itoa(code)),
	} {
		f, err := root.OpenFile(tmp+"/"+name, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			_ = root.RemoveAll(tmp)
			return
		}
		_, werr := f.Write(data)
		cerr := f.Close()
		if werr != nil || cerr != nil {
			_ = root.RemoveAll(tmp)
			return
		}
	}
	final := "resp/" + id
	_ = root.RemoveAll(final)
	if err := root.Rename(tmp, final); err != nil {
		_ = root.RemoveAll(tmp)
	}
}

// readRelayFile reads a request file that must be a regular file (not a
// symlink) within the root. tooBig reports a file over sandboxRelayMaxBytes.
func readRelayFile(root *os.Root, name string) (data []byte, tooBig bool, err error) {
	fi, err := root.Lstat(name)
	if err != nil {
		return nil, false, err
	}
	if !fi.Mode().IsRegular() {
		return nil, false, errors.New("not a regular file")
	}
	f, err := root.OpenFile(name, os.O_RDONLY|syscall.O_NONBLOCK, 0) // non-blocking so a swapped-in FIFO cannot hang the open
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = f.Close() }() // read-only file handle
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		return nil, false, errors.New("not a regular file")
	}
	data, err = io.ReadAll(io.LimitReader(f, sandboxRelayMaxBytes+1))
	if err != nil {
		return nil, false, err
	}
	if len(data) > sandboxRelayMaxBytes {
		return nil, true, nil
	}
	return data, false, nil
}

func splitRelayArgv(raw []byte) []string {
	s := strings.TrimSuffix(string(raw), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// cappedBuffer keeps at most max bytes and discards the rest.
type cappedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (c *cappedBuffer) Write(p []byte) (int, error) {
	if room := c.max - c.buf.Len(); room < len(p) {
		c.truncated = true
		if room > 0 {
			c.buf.Write(p[:room])
		}
	} else {
		c.buf.Write(p)
	}
	return len(p), nil
}

// hostAORunner runs the host `ao` binary with the supervisor's environment,
// which is the session's host env, so AO_* identity comes from the host and
// never from the container.
func hostAORunner(exe string) relayRunFunc {
	return func(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, int) {
		cmd := aoprocess.CommandContext(ctx, exe, argv...)
		aoprocess.ConfigureTreeCancellation(cmd)
		cmd.WaitDelay = sandboxRelayWaitDelay
		cmd.Stdin = bytes.NewReader(stdin)
		out := &cappedBuffer{max: sandboxRelayMaxBytes}
		errb := &cappedBuffer{max: sandboxRelayMaxBytes}
		cmd.Stdout, cmd.Stderr = out, errb
		runErr := cmd.Run()
		code := relayExitCode(ctx, cmd, runErr)
		if out.truncated || errb.truncated {
			errb.buf.WriteString("\n[output truncated]\n")
		}
		if runErr != nil && cmd.ProcessState == nil {
			errb.buf.WriteString("ao: relay: " + runErr.Error() + "\n")
		}
		return out.buf.Bytes(), errb.buf.Bytes(), code
	}
}

// relayExitCode maps a finished command to a shell-style exit code: 124 for a
// deadline, 128+signal for a signal death, and never a negative value.
func relayExitCode(ctx context.Context, cmd *exec.Cmd, runErr error) int {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return 124
	}
	if cmd.ProcessState == nil {
		if runErr != nil {
			return 1
		}
		return 0
	}
	if ws, ok := cmd.ProcessState.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
		return 128 + int(ws.Signal())
	}
	if code := cmd.ProcessState.ExitCode(); code >= 0 {
		return code
	}
	return 1
}
