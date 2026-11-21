package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/daemonmeta"
	"github.com/aoagents/agent-orchestrator/backend/internal/runfile"
)

const probeTimeout = 2 * time.Second

// maxProbeBody caps a /healthz or /readyz response. The daemon's is a few
// hundred bytes; the cap keeps an unverified process on the run-file's port
// from making the CLI buffer an arbitrarily large body.
const maxProbeBody = 64 << 10

type statusOptions struct {
	json bool
}

type daemonState string

const (
	stateReady     daemonState = "ready"
	stateStopped   daemonState = "stopped"
	stateStale     daemonState = "stale"
	stateUnhealthy daemonState = "unhealthy"
	stateNotReady  daemonState = "not_ready"
)

type daemonStatus struct {
	State daemonState `json:"state"`
	PID   int         `json:"pid,omitempty"`
	Port  int         `json:"port,omitempty"`
	// ExecutablePath is the binary the running daemon reports for itself, read
	// from its own /healthz. It is the authority on which `ao` the app uses,
	// which the CLI's own os.Executable is not: `ao doctor` may itself be a
	// different install than the daemon the app started.
	ExecutablePath string     `json:"executablePath,omitempty"`
	StartedAt      *time.Time `json:"startedAt,omitempty"`
	Uptime         string     `json:"uptime,omitempty"`
	RunFile        string     `json:"runFile"`
	DataDir        string     `json:"dataDir"`
	Health         string     `json:"health,omitempty"`
	Ready          string     `json:"ready,omitempty"`
	Error          string     `json:"error,omitempty"`
	owned          bool
	// workingDirectory is the daemon's own working directory from /healthz.
	// The daemon chdirs into its data dir, so this identifies the data dir it
	// actually serves (empty for daemons that do not report it).
	workingDirectory string
	// pidVisible records whether inspection saw the daemon's PID alive, as
	// opposed to verifying it only over loopback (probeOwnedDaemon). Checking
	// again later could match an unrelated process reusing that PID number.
	pidVisible bool
}

type probeResult struct {
	Status           string `json:"status"`
	Service          string `json:"service"`
	PID              int    `json:"pid"`
	ExecutablePath   string `json:"executablePath,omitempty"`
	WorkingDirectory string `json:"workingDirectory,omitempty"`
}

func newStatusCommand(ctx *commandContext) *cobra.Command {
	var opts statusOptions
	cmd := &cobra.Command{
		Use:   "status",
		Short: "Show Orchestrator daemon status",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := ctx.inspectDaemon(cmd.Context())
			if err != nil {
				return err
			}
			if opts.json {
				return writeJSON(cmd.OutOrStdout(), st)
			}
			return writeStatus(cmd, st)
		},
	}
	cmd.Flags().BoolVar(&opts.json, "json", false, "Output status as JSON")
	return cmd
}

func (c *commandContext) inspectDaemon(ctx context.Context) (daemonStatus, error) {
	cfg, err := config.Load()
	if err != nil {
		return daemonStatus{}, err
	}
	st := daemonStatus{State: stateStopped, RunFile: cfg.RunFilePath, DataDir: cfg.DataDir}

	info, err := runfile.Read(cfg.RunFilePath)
	if err != nil {
		return daemonStatus{}, err
	}
	if info == nil {
		return st, nil
	}

	st.PID = info.PID
	st.Port = info.Port
	startedAt := info.StartedAt
	st.StartedAt = &startedAt
	st.Uptime = formatUptime(c.deps.Now().Sub(info.StartedAt))

	var health probeResult
	st.pidVisible = c.deps.ProcessAlive(info.PID)
	if st.pidVisible {
		health, err = c.readProbe(ctx, info.Port, "healthz")
		if err != nil {
			st.State = stateUnhealthy
			st.Error = err.Error()
			return st, nil
		}
		if err := verifyProbeOwner(health, info.PID, "healthz"); err != nil {
			st.State = stateStale
			st.Error = err.Error()
			return st, nil
		}
	} else {
		// The PID may only be hidden, not dead: see probeOwnedDaemon.
		probe, err := c.probeOwnedDaemon(ctx, info)
		if err != nil {
			if c.pidMayBeHidden() && probeTimedOut(err) {
				// A probe that times out proves neither a dead daemon nor a
				// live one. Calling it stale would let `ao stop` delete the
				// run-file of a daemon that is still running.
				st.State = stateUnhealthy
				st.Error = fmt.Sprintf("daemon pid %d is not visible from here and %v", info.PID, err)
				return st, nil
			}
			st.State = stateStale
			st.Error = "run-file points to a dead process"
			return st, nil
		}
		health = probe
	}
	st.owned = true
	st.ExecutablePath = health.ExecutablePath
	st.workingDirectory = health.WorkingDirectory
	st.Health = health.Status
	if health.Status != "ok" {
		st.State = stateUnhealthy
		return st, nil
	}

	ready, err := c.readProbe(ctx, info.Port, "readyz")
	if err != nil {
		st.State = stateNotReady
		st.Error = err.Error()
		return st, nil
	}
	if err := verifyProbeOwner(ready, info.PID, "readyz"); err != nil {
		st.State = stateStale
		st.owned = false
		st.Error = err.Error()
		return st, nil
	}
	st.Ready = ready.Status
	if ready.Status == string(stateReady) {
		st.State = stateReady
		return st, nil
	}
	st.State = stateNotReady
	return st, nil
}

func (c *commandContext) readProbe(ctx context.Context, port int, path string) (probeResult, error) {
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, fmt.Sprintf("http://%s:%d/%s", config.LoopbackHost, port, path), http.NoBody)
	if err != nil {
		return probeResult{}, err
	}
	// Never follow a redirect: the answer must come from the run-file's
	// loopback port itself, and an unverified process there must not be able
	// to send the CLI to another host.
	client := *c.deps.HTTPClient
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	resp, err := client.Do(req)
	if err != nil {
		return probeResult{}, fmt.Errorf("%s: %w", path, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return probeResult{}, fmt.Errorf("%s: HTTP %d", path, resp.StatusCode)
	}
	var body probeResult
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxProbeBody)).Decode(&body); err != nil {
		return probeResult{}, fmt.Errorf("%s: decode response: %w", path, err)
	}
	if body.Status == "" {
		return probeResult{}, fmt.Errorf("%s: missing status", path)
	}
	return body, nil
}

// probeOwnedDaemon asks the run-file's loopback port whether the daemon it
// records is still serving. It is the fallback for a failed PID check: a CLI
// running inside a sandbox with its own PID namespace (Codex on Linux runs
// every shell command in one) cannot see the daemon's process, although the
// daemon answers on loopback. The daemon counts as alive only when /healthz
// identifies itself as the Orchestrator daemon with the run-file's PID, so a
// different process that reuses the port is not mistaken for it.
//
// That claim is unauthenticated, so it is only consulted where a PID can be
// hidden but alive (Deps.PIDMayBeHidden). Elsewhere a failed PID check means
// the daemon is dead, and the run-file is stale whatever answers on its port.
func (c *commandContext) probeOwnedDaemon(ctx context.Context, info *runfile.Info) (probeResult, error) {
	if !c.pidMayBeHidden() {
		return probeResult{}, errPIDCannotBeHidden
	}
	if info == nil || info.PID <= 0 || info.Port <= 0 {
		return probeResult{}, errors.New("run-file has no daemon pid or port")
	}
	health, err := c.readProbe(ctx, info.Port, "healthz")
	if err != nil {
		return probeResult{}, err
	}
	if err := verifyProbeOwner(health, info.PID, "healthz"); err != nil {
		return probeResult{}, err
	}
	return health, nil
}

var errPIDCannotBeHidden = errors.New("a dead pid cannot be hidden on this platform")

func (c *commandContext) pidMayBeHidden() bool {
	return c.deps.PIDMayBeHidden != nil && c.deps.PIDMayBeHidden()
}

// probeTimedOut reports whether a probe failed by timing out, which says
// nothing about whether the daemon is alive.
func probeTimedOut(err error) bool {
	var netErr net.Error
	return errors.Is(err, context.DeadlineExceeded) || (errors.As(err, &netErr) && netErr.Timeout())
}

// daemonAlive reports whether the daemon the run-file records is running: its
// PID is visible and alive, or, when it is not, the daemon itself answers on
// loopback (probeOwnedDaemon).
func (c *commandContext) daemonAlive(ctx context.Context, info *runfile.Info) bool {
	if c.deps.ProcessAlive(info.PID) {
		return true
	}
	_, err := c.probeOwnedDaemon(ctx, info)
	return err == nil
}

func verifyProbeOwner(probe probeResult, wantPID int, path string) error {
	if probe.Service != daemonmeta.ServiceName {
		return fmt.Errorf("%s: response is not from the Orchestrator daemon", path)
	}
	if probe.PID != wantPID {
		return fmt.Errorf("%s: daemon pid %d does not match run-file pid %d", path, probe.PID, wantPID)
	}
	return nil
}

func writeStatus(cmd *cobra.Command, st daemonStatus) error {
	out := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(out, "Orchestrator daemon: %s\n", st.State); err != nil {
		return err
	}
	if st.PID != 0 {
		if _, err := fmt.Fprintf(out, "  pid: %d\n", st.PID); err != nil {
			return err
		}
	}
	if st.Port != 0 {
		if _, err := fmt.Fprintf(out, "  port: %d\n", st.Port); err != nil {
			return err
		}
	}
	if st.StartedAt != nil && !st.StartedAt.IsZero() {
		if _, err := fmt.Fprintf(out, "  started: %s\n", st.StartedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if st.Uptime != "" {
		if _, err := fmt.Fprintf(out, "  uptime: %s\n", st.Uptime); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintf(out, "  run file: %s\n", st.RunFile); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(out, "  data dir: %s\n", st.DataDir); err != nil {
		return err
	}
	if st.Health != "" {
		if _, err := fmt.Fprintf(out, "  healthz: %s\n", st.Health); err != nil {
			return err
		}
	}
	if st.Ready != "" {
		if _, err := fmt.Fprintf(out, "  readyz: %s\n", st.Ready); err != nil {
			return err
		}
	}
	if st.Error != "" {
		if _, err := fmt.Fprintf(out, "  error: %s\n", st.Error); err != nil {
			return err
		}
	}
	return nil
}

func formatUptime(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return d.Round(time.Second).String()
}
