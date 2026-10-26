package coder

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/activity"
	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/sandbox"
	"github.com/coder/websocket"
	"github.com/google/uuid"
)

const (
	// readyProbeMarker prefixes the line that reports the ready check's exit
	// status after its output.
	readyProbeMarker = "__AO_READY_PROBE__"
	// defaultReadyProbeInterval paces ready-check attempts.
	defaultReadyProbeInterval = 5 * time.Second
	// defaultReadyProbeInlineWindow bounds how long one bootstrap call polls
	// before returning "not ready yet": the reconciler retries a few seconds
	// later, so a long template setup never pins a reconcile slot.
	defaultReadyProbeInlineWindow = 30 * time.Second
	// readyProbeCommandTimeout bounds one run of the ready check inside the
	// workspace (enforced with timeout(1) when the image has it).
	readyProbeCommandTimeout = 60
	// readyProbeAttemptWait bounds how long AO waits for one attempt to report.
	readyProbeAttemptWait = readyProbeCommandTimeout*time.Second + 15*time.Second
	// readyProbeOutputLimit bounds the probe output kept for diagnostics, and
	// readyProbeMessageLimit the excerpt shown to users.
	readyProbeOutputLimit  = 2048
	readyProbeMessageLimit = 400
)

// readyProbeFirstSeen remembers when this process first probed an agent
// whose Coder API did not report ready_at, so the timeout still has an anchor.
var readyProbeFirstSeen sync.Map // agent ID -> time.Time

type readyProbeResult struct {
	exitCode int
	output   string
	// raw is the command's output before the marker as the terminal sent
	// it, up to the keep bytes runMarkedCommandOnce was asked for.
	raw string
}

// waitForReadyProbe runs the connection's ready check as the workspace user
// until it exits 0. The timeout is measured from when the Coder agent became
// ready, so it spans bootstrap attempts. One call polls for at most the inline
// window and then reports the workspace as not ready (retry later); once the
// timeout has passed it fails with workspace_setup_not_ready, carrying the
// check's last output, bounded and redacted.
func (c *Client) waitForReadyProbe(ctx context.Context, id sandbox.ID, ptyURL *url.URL, agent workspaceAgent) error {
	finish := sandbox.BeginStage(ctx, sandbox.StageReadyProbe)
	attrs := c.stageAttrs(string(id), agent.ID)
	attrs["timeout_seconds"] = strconv.Itoa(int(c.readyProbeTimeout / time.Second))
	now := c.now
	if now == nil {
		now = time.Now
	}
	interval := c.readyProbeInterval
	if interval <= 0 {
		interval = defaultReadyProbeInterval
	}
	window := c.readyProbeInlineWindow
	if window <= 0 {
		window = defaultReadyProbeInlineWindow
	}
	timeout := c.readyProbeTimeout
	if timeout <= 0 {
		timeout = time.Duration(domain.DefaultCoderReadyProbeTimeoutSeconds) * time.Second
	}
	deadline := readyProbeAnchor(agent, now()).Add(timeout)
	inlineUntil := now().Add(window)
	attempts := 0
	var last readyProbeResult
	for {
		attempts++
		result, err := c.runReadyProbe(ctx, ptyURL)
		attrs["attempts"] = strconv.Itoa(attempts)
		if err != nil {
			if ctx.Err() != nil {
				finish(ctx.Err(), attrs)
				return ctx.Err()
			}
			err = classifyBootstrapError(err, "")
			finish(err, attrs)
			return err
		}
		last = result
		attrs["exit_code"] = strconv.Itoa(result.exitCode)
		if result.exitCode == 0 {
			finish(nil, attrs)
			return nil
		}
		current := now()
		if !current.Before(deadline) {
			err := readyProbeTimeoutError(c.readyProbe, timeout, last)
			attrs["output"] = activity.Excerpt(last.output, 512)
			finish(err, attrs)
			return err
		}
		if !current.Add(interval).Before(inlineUntil) {
			err := fmt.Errorf("coder: workspace ready check has not passed yet (exit %d, %s left): %w",
				last.exitCode, deadline.Sub(current).Round(time.Second), sandbox.ErrWorkspaceNotReady)
			finish(err, attrs)
			return err
		}
		timer := time.NewTimer(interval)
		select {
		case <-ctx.Done():
			timer.Stop()
			finish(ctx.Err(), attrs)
			return ctx.Err()
		case <-timer.C:
		}
	}
}

// readyProbeAnchor is when the ready check's timeout starts: the agent's
// ready_at, or the first time this process probed the agent.
func readyProbeAnchor(agent workspaceAgent, now time.Time) time.Time {
	if agent.ReadyAt != nil && !agent.ReadyAt.IsZero() {
		return *agent.ReadyAt
	}
	first, _ := readyProbeFirstSeen.LoadOrStore(agent.ID, now)
	return first.(time.Time)
}

func readyProbeTimeoutError(probe string, timeout time.Duration, last readyProbeResult) error {
	excerpt := activity.Excerpt(last.output, readyProbeMessageLimit)
	message := fmt.Sprintf("The workspace's own setup didn't finish within %s: its ready check kept failing (exit %d), so AO didn't start its worker.",
		humanReadyProbeDuration(timeout), last.exitCode)
	if excerpt != "" {
		message += " Last output: " + excerpt
	}
	return &sandbox.StartupError{
		Code:    sandbox.StartupErrorWorkspaceSetupNotReady,
		Message: message,
		Err: fmt.Errorf("coder: ready check %q did not pass within %s (last exit %d): %s",
			activity.Excerpt(probe, 200), timeout, last.exitCode, activity.Excerpt(last.output, readyProbeOutputLimit)),
	}
}

func humanReadyProbeDuration(d time.Duration) string {
	if d >= time.Minute && d%time.Minute == 0 {
		if minutes := int(d / time.Minute); minutes != 1 {
			return fmt.Sprintf("%d minutes", minutes)
		}
		return "1 minute"
	}
	return d.String()
}

// readyProbeScript runs the ready check once in a login shell, bounded by
// timeout(1) when available, then prints the tail of its output and its exit
// status after readyProbeMarker.
func readyProbeScript(probe string) string {
	return "ao_probe=" + shellQuote(probe) + "\n" +
		"if command -v timeout >/dev/null 2>&1; then\n" +
		"  ao_out=$(timeout " + strconv.Itoa(readyProbeCommandTimeout) + " sh -c \"$ao_probe\" 2>&1 </dev/null)\n" +
		"else\n" +
		"  ao_out=$(sh -c \"$ao_probe\" 2>&1 </dev/null)\n" +
		"fi\n" +
		"ao_code=$?\n" +
		"printf '%s\\n' \"$ao_out\" | tail -c " + strconv.Itoa(readyProbeOutputLimit) + "\n" +
		"printf '\\n%s:%s\\n' " + readyProbeMarker + " \"$ao_code\"\n"
}

// runReadyProbe runs the ready check once through the agent PTY, as the user
// the Coder agent runs as. Terminal failures are retried briefly, then
// reported as errTerminalNotReady (retry later).
func (c *Client) runReadyProbe(ctx context.Context, ptyURL *url.URL) (readyProbeResult, error) {
	attemptURL := *ptyURL
	query := attemptURL.Query()
	query.Set("width", "200")
	query.Set("height", "50")
	query.Set("command", "sh -lc "+shellQuote(readyProbeScript(c.readyProbe)))
	query.Set("backend_type", "buffered")
	attemptURL.RawQuery = query.Encode()
	const attempts = 3
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		result, err := c.runReadyProbeOnce(ctx, &attemptURL)
		if err == nil {
			return result, nil
		}
		if ctx.Err() != nil {
			return readyProbeResult{}, ctx.Err()
		}
		lastErr = err
		if attempt+1 < attempts {
			if err := sleepPTYBackoff(ctx, attempt, err); err != nil {
				return readyProbeResult{}, err
			}
		}
	}
	return readyProbeResult{}, lastErr
}

func (c *Client) runReadyProbeOnce(ctx context.Context, ptyURL *url.URL) (readyProbeResult, error) {
	result, _, err := c.runMarkedCommandOnce(ctx, ptyURL, readyProbeMarker, readyProbeAttemptWait, 2*readyProbeOutputLimit)
	return result, err
}

// runMarkedCommandOnce opens the agent PTY for a command that prints its
// output and then a "<marker>:<status>" line, and returns the output before
// the marker, the status parsed as an exit code (-1 when it is not a number),
// and the raw status. It keeps at least the last keep bytes of the output and
// waits at most wait for the marker.
func (c *Client) runMarkedCommandOnce(ctx context.Context, ptyURL *url.URL, marker string, wait time.Duration, keep int) (readyProbeResult, string, error) {
	attemptURL := *ptyURL
	query := attemptURL.Query()
	query.Set("reconnect", uuid.NewString())
	attemptURL.RawQuery = query.Encode()
	conn, response, err := websocket.Dial(ctx, attemptURL.String(), &websocket.DialOptions{
		HTTPClient: c.http, HTTPHeader: http.Header{"Coder-Session-Token": []string{c.token}},
	})
	if err != nil {
		if response != nil {
			response.Body.Close()
			return readyProbeResult{}, "", fmt.Errorf("%w: open workspace PTY returned %d", errTerminalNotReady, response.StatusCode)
		}
		return readyProbeResult{}, "", fmt.Errorf("%w: open workspace PTY: %v", errTerminalNotReady, err)
	}
	streamCtx, stopStream := context.WithCancel(ctx)
	netConn := websocket.NetConn(streamCtx, conn, websocket.MessageBinary)
	output, outputDone := streamPTYOutput(streamCtx, netConn)
	defer func() {
		stopStream()
		_ = conn.CloseNow()
		<-outputDone
	}()
	timer := time.NewTimer(wait)
	defer timer.Stop()
	var collected strings.Builder
	for {
		select {
		case <-ctx.Done():
			return readyProbeResult{}, "", ctx.Err()
		case <-timer.C:
			return readyProbeResult{}, "", fmt.Errorf("%w: workspace command did not report", errTerminalNotReady)
		case line, ok := <-output:
			if !ok {
				return readyProbeResult{}, "", fmt.Errorf("%w: workspace PTY closed before the command reported", errTerminalNotReady)
			}
			if before, rest, found := strings.Cut(line.data, marker+":"); found {
				collected.WriteString(before)
				status := strings.TrimSpace(rest)
				code, convErr := strconv.Atoi(status)
				if convErr != nil {
					code = -1
				}
				raw := collected.String()
				if len(raw) > keep {
					raw = raw[len(raw)-keep:]
				}
				return readyProbeResult{exitCode: code, output: boundedProbeOutput(collected.String()), raw: raw}, status, nil
			}
			collected.WriteString(line.data)
			if collected.Len() > 2*keep {
				tail := collected.String()[collected.Len()-keep:]
				collected.Reset()
				collected.WriteString(tail)
			}
			if line.err != nil {
				return readyProbeResult{}, "", errors.Join(errTerminalNotReady, fmt.Errorf("read workspace command output: %w", line.err))
			}
		}
	}
}

// boundedProbeOutput keeps the redacted, control-character-free tail of the
// ready check's output.
func boundedProbeOutput(output string) string {
	output = activity.Redact(strings.ReplaceAll(output, "\r", ""))
	output = strings.Map(func(character rune) rune {
		if character == '\n' || character == '\t' || character >= ' ' {
			return character
		}
		return -1
	}, output)
	output = strings.TrimSpace(output)
	if len(output) > readyProbeOutputLimit {
		output = output[len(output)-readyProbeOutputLimit:]
	}
	return output
}
