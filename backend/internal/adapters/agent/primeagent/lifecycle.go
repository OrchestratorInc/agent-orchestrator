package primeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	primeCommandTimeout   = 10 * time.Second
	maxPrimeCommandOutput = 4 << 10
)

type primeCommandRunner func(ctx context.Context, binary, workingDir string, env map[string]string, args ...string) ([]byte, error)

type primeListResponse struct {
	Sessions []primeSessionSummary `json:"sessions"`
}

type primeSessionSummary struct {
	SessionID       string `json:"sessionId"`
	ActiveSessionID string `json:"activeSessionId"`
	Activity        string `json:"activity"`
	CWD             string `json:"cwd"`
	Created         string `json:"created"`
	RLMDepth        int    `json:"rlmDepth"`
	RuntimeKind     string `json:"runtimeKind"`
}

func runPrimeCommand(ctx context.Context, binary, workingDir string, env map[string]string, args ...string) ([]byte, error) {
	cmd := aoprocess.CommandContext(ctx, binary, args...) //nolint:gosec // binary is adapter-resolved; args are adapter-owned
	if strings.TrimSpace(workingDir) != "" {
		cmd.Dir = workingDir
	}
	// The socket comes only from env: an inherited one would point the
	// default-socket probe at some other daemon.
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "PRIME_AGENT_DAEMON_SOCKET=") {
			cmd.Env = append(cmd.Env, kv)
		}
	}
	for key, value := range env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}
	return cmd.CombinedOutput()
}

func (p *Plugin) executePrimeCommand(ctx context.Context, dataDir string, args ...string) ([]byte, error) {
	return p.executePrimeCommandOn(ctx, dataDir, true, args...)
}

// executePrimeCommandOn runs a Prime command against AO's private daemon
// socket, or, with privateSocket false, against the socket Prime picks by
// default for AO's state root. Sessions started before AO pinned the private
// socket still live on that default one.
func (p *Plugin) executePrimeCommandOn(ctx context.Context, dataDir string, privateSocket bool, args ...string) ([]byte, error) {
	binary, err := p.primeAgentBinary(ctx)
	if err != nil {
		return nil, err
	}
	dir, err := primeDataDir(dataDir)
	if err != nil {
		return nil, err
	}
	env := map[string]string{primeAgentCodingAgentDirEnv: dir}
	if privateSocket {
		env["PRIME_AGENT_DAEMON_SOCKET"] = primeDaemonSocket(dir)
	}
	runner := p.runCommand
	if runner == nil {
		runner = runPrimeCommand
	}
	runCtx, cancel := context.WithTimeout(ctx, primeCommandTimeout)
	defer cancel()
	return runner(runCtx, binary, dir, env, args...)
}

func primeDaemonSocket(primeDir string) string {
	return filepath.Join(primeDir, "daemon.sock")
}

func activeSessionForTranscript(output []byte, nativeID string) (string, bool, error) {
	var response primeListResponse
	if err := json.Unmarshal(output, &response); err != nil {
		return "", false, fmt.Errorf("decode session list: %w", err)
	}
	var matches []string
	for _, session := range response.Sessions {
		if strings.TrimSpace(session.SessionID) == nativeID && strings.TrimSpace(session.ActiveSessionID) != "" {
			matches = append(matches, strings.TrimSpace(session.ActiveSessionID))
		}
	}
	if len(matches) == 0 {
		return "", false, nil
	}
	if len(matches) != 1 {
		return "", false, fmt.Errorf("multiple live Prime sessions match transcript %q", nativeID)
	}
	return matches[0], true, nil
}

func boundedPrimeOutput(output []byte) string {
	if len(output) > maxPrimeCommandOutput {
		output = output[:maxPrimeCommandOutput]
	}
	return strings.TrimSpace(string(output))
}

// TerminateNativeSession stops the live Prime worker that owns session's
// transcript. An inactive transcript is already safe and remains resumable.
//
// AO's private daemon socket is checked first. A session started before AO
// pinned that socket still runs on Prime's default one, so when the private
// socket is absent or does not own the transcript, the default daemon is asked
// too. The default daemon not running is only an error when there is no
// private daemon either, which matches the behavior before the private socket.
func (p *Plugin) TerminateNativeSession(ctx context.Context, session ports.SessionRef) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	nativeID := strings.TrimSpace(session.Metadata[ports.MetadataKeyAgentSessionID])
	if nativeID == "" {
		return nil
	}
	dir, err := primeDataDir(session.DataDir)
	if err != nil {
		return err
	}
	var privateErr error
	privatePresent := false
	if _, statErr := os.Stat(primeDaemonSocket(dir)); statErr == nil {
		privatePresent = true
		stopped, err := p.stopTranscriptOwner(ctx, session.DataDir, true, nativeID)
		if stopped || (err != nil && ctx.Err() != nil) {
			return err
		}
		privateErr = err
	}
	stopped, err := p.stopTranscriptOwner(ctx, session.DataDir, false, nativeID)
	if stopped {
		return err
	}
	if privateErr != nil {
		return privateErr
	}
	if err != nil && !privatePresent {
		return err
	}
	return nil
}

// stopTranscriptOwner lists one daemon's live sessions and stops the one that
// owns nativeID. It reports stopped once a stop was attempted, so the caller
// does not ask another daemon about a transcript this one owns.
func (p *Plugin) stopTranscriptOwner(ctx context.Context, dataDir string, privateSocket bool, nativeID string) (bool, error) {
	output, err := p.executePrimeCommandOn(ctx, dataDir, privateSocket, "list", "--json")
	if err != nil {
		return false, fmt.Errorf("prime-agent: list live sessions: %w: %s", err, boundedPrimeOutput(output))
	}
	activeID, ok, err := activeSessionForTranscript(output, nativeID)
	if err != nil || !ok {
		return false, err
	}
	output, err = p.executePrimeCommandOn(ctx, dataDir, privateSocket, "stop", activeID, "--json")
	if err != nil {
		return true, fmt.Errorf("prime-agent: stop active session %q: %w: %s", activeID, err, boundedPrimeOutput(output))
	}
	return true, nil
}
