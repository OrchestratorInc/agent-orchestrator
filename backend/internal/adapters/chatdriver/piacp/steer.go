package piacp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net"
	"path/filepath"
	"runtime"
	"time"

	acpsdk "github.com/coder/acp-go-sdk"

	acpdriver "github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/acp"
)

// steerDirEnv names the folder where AO's Pi extension (agent/pi/hooks.go)
// listens for steers, one socket per Pi session. It is the session-private
// folder that already holds pi-acp's daemon socket.
const steerDirEnv = "AO_PI_STEER_DIR"

// steerTimeout bounds one steer: connecting, and the extension's answer. The
// extension answers at once; it only queues the message.
const steerTimeout = 3 * time.Second

// steerExternal steers Pi through AO's own Pi extension rather than ACP:
// pi-acp queues a second prompt until the turn ends, while Pi's extension API
// delivers a steer after the running tool calls (pi.sendMessage with
// deliverAs "steer"). Steer is offered only when the session's socket is up.
var steerExternal = &acpdriver.SteerExternal{
	Supported: func(init acpsdk.InitializeResponse) bool {
		return runtime.GOOS != "windows" && init.AgentInfo != nil && init.AgentInfo.Name == distributionName
	},
	Open: func(cfg acpdriver.LaunchConfig) acpdriver.SteerChannel {
		return steerChannel{dir: sessionRunDir(cfg)}
	},
}

// sessionRunDir is the session-private folder for pi-acp's daemon socket and
// AO's steering sockets.
func sessionRunDir(cfg acpdriver.LaunchConfig) string {
	return filepath.Join(cfg.DataDir, "pi-acp", string(cfg.SessionID), "run")
}

type steerChannel struct{ dir string }

// steerSocketPath matches the socket AO's Pi extension opens: one per Pi
// process, and pi-acp runs one process per AO session.
func steerSocketPath(dir string) string {
	return filepath.Join(dir, "ao-steer.sock")
}

// Ready reports whether the extension is listening. It connects rather than
// checking for the file, which a Pi process that died can leave behind.
func (c steerChannel) Ready(string) bool {
	conn, err := net.DialTimeout("unix", steerSocketPath(c.dir), 250*time.Millisecond)
	if err != nil {
		return false
	}
	_ = conn.Close()
	return true
}

type steerReply struct {
	Outcome string `json:"outcome"`
	Reason  string `json:"reason,omitempty"`
}

func (c steerChannel) Steer(ctx context.Context, sessionID, text string) (acpdriver.SteerOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, steerTimeout)
	defer cancel()
	var dialer net.Dialer
	conn, err := dialer.DialContext(ctx, "unix", steerSocketPath(c.dir))
	if err != nil {
		// Nothing was sent, so this is a definite "not delivered".
		return 0, acpdriver.RefuseSteer(fmt.Errorf("pi steering is not reachable: %w", err))
	}
	defer func() { _ = conn.Close() }()
	if deadline, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(deadline)
	}
	request, err := json.Marshal(map[string]string{"sessionId": sessionID, "text": text})
	if err != nil {
		return 0, fmt.Errorf("encode pi steer: %w", err)
	}
	if _, err := conn.Write(append(request, '\n')); err != nil {
		return 0, fmt.Errorf("send pi steer: %w", err)
	}
	line, err := bufio.NewReader(conn).ReadBytes('\n')
	if len(line) == 0 {
		// The message may have been queued before the answer was lost.
		return 0, fmt.Errorf("read pi steer answer: %w", err)
	}
	var reply steerReply
	if err := json.Unmarshal(line, &reply); err != nil {
		return 0, fmt.Errorf("decode pi steer answer %q: %w", line, err)
	}
	switch reply.Outcome {
	case "injected":
		return acpdriver.SteerInjected, nil
	case "promptRequired":
		return acpdriver.SteerNoRunningTurn, nil
	case "error":
		return 0, acpdriver.RefuseSteer(fmt.Errorf("pi refused the steer: %s", reply.Reason))
	default:
		return 0, fmt.Errorf("pi steering returned unknown outcome %q", reply.Outcome)
	}
}
