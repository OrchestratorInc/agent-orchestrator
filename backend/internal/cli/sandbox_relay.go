package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	sandboxRelayPoll     = 50 * time.Millisecond
	sandboxRelayTimeout  = 60 * time.Second
	sandboxRelayMaxBytes = 8 << 20
)

var sandboxRelayIDPattern = regexp.MustCompile(`^[0-9]+-[0-9]+-[0-9]+$`)

// sandboxRelayPolicy decides which top-level `ao` commands a sandboxed agent
// may run on the host (#6663). Every root command must be listed. Denied
// commands either execute arbitrary host argv (launch, agent-process,
// pty-host, chat-host), manage the daemon or credentials, or could switch the
// sandbox off (project).
var sandboxRelayPolicy = map[string]bool{
	"hooks": true, "report": true, "send": true, "spawn": true, "preview": true,
	"render": true, "browser": true, "session": true, "orchestrator": true,
	"pr": true, "review": true, "automation": true, "cue": true, "status": true,
	"version": true,

	"daemon": false, "start": false, "stop": false, "remote-host": false,
	"doctor": false, "agent": false, "mcp": false, "agent-process": false,
	"chat-host": false, "unreal-provider": false, "launch": false,
	"pty-host": false, "codex-login": false, "claude-login": false,
	"import": false, "dev": false, "project": false, "completion": false,
}

type relayRunFunc func(ctx context.Context, argv []string, stdin []byte) (stdout, stderr []byte, code int)

// serveSandboxRelay answers relay requests under dir until ctx ends.
func serveSandboxRelay(ctx context.Context, dir string, run relayRunFunc) {
	ticker := time.NewTicker(sandboxRelayPoll)
	defer ticker.Stop()
	for {
		entries, _ := os.ReadDir(filepath.Join(dir, "req"))
		for _, e := range entries {
			id := e.Name()
			if !e.IsDir() || !sandboxRelayIDPattern.MatchString(id) {
				continue
			}
			work := filepath.Join(dir, "work", id)
			// Rename claims the request so it is handled exactly once.
			if err := os.Rename(filepath.Join(dir, "req", id), work); err != nil {
				continue
			}
			go handleSandboxRelay(ctx, dir, id, work, run)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func handleSandboxRelay(ctx context.Context, dir, id, work string, run relayRunFunc) {
	defer os.RemoveAll(work)
	argv := splitRelayArgv(readCapped(filepath.Join(work, "argv")))
	stdin := readCapped(filepath.Join(work, "stdin"))
	var stdout, stderr []byte
	code := 126
	if len(argv) == 0 || !sandboxRelayPolicy[argv[0]] {
		name := ""
		if len(argv) > 0 {
			name = argv[0]
		}
		stderr = []byte(fmt.Sprintf("ao: %q is not allowed in the sandbox\n", name))
	} else {
		rctx, cancel := context.WithTimeout(ctx, sandboxRelayTimeout)
		stdout, stderr, code = run(rctx, argv, stdin)
		cancel()
	}
	tmp := filepath.Join(dir, "resp", id+".tmp")
	if err := os.MkdirAll(tmp, 0o700); err != nil {
		return
	}
	_ = os.WriteFile(filepath.Join(tmp, "stdout"), stdout, 0o600)
	_ = os.WriteFile(filepath.Join(tmp, "stderr"), stderr, 0o600)
	_ = os.WriteFile(filepath.Join(tmp, "exit"), []byte(strconv.Itoa(code)), 0o600)
	_ = os.Rename(tmp, filepath.Join(dir, "resp", id))
}

func readCapped(path string) []byte {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()
	data, _ := io.ReadAll(io.LimitReader(f, sandboxRelayMaxBytes))
	return data
}

func splitRelayArgv(raw []byte) []string {
	s := strings.TrimSuffix(string(raw), "\x00")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\x00")
}

// hostAORunner runs the host `ao` binary with the supervisor's environment,
// which is the session's host env, so AO_* identity comes from the host and
// never from the container.
func hostAORunner(exe string) relayRunFunc {
	return func(ctx context.Context, argv []string, stdin []byte) ([]byte, []byte, int) {
		cmd := aoprocess.CommandContext(ctx, exe, argv...)
		cmd.Stdin = bytes.NewReader(stdin)
		var out, errb bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &errb
		if err := cmd.Run(); err != nil {
			if cmd.ProcessState != nil {
				return out.Bytes(), errb.Bytes(), cmd.ProcessState.ExitCode()
			}
			return out.Bytes(), append(errb.Bytes(), []byte("ao: relay: "+err.Error()+"\n")...), 1
		}
		return out.Bytes(), errb.Bytes(), 0
	}
}
