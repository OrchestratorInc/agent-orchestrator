package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"maps"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/tmuxbin"
)

type doctorLevel string

const (
	doctorPass doctorLevel = "PASS"
	doctorWarn doctorLevel = "WARN"
	doctorFail doctorLevel = "FAIL"
)

type doctorCheck struct {
	Level   doctorLevel `json:"level"`
	Section string      `json:"section,omitempty"`
	Name    string      `json:"name"`
	Message string      `json:"message"`
}

type doctorReport struct {
	OK       bool          `json:"ok"`
	Failures int           `json:"failures"`
	Checks   []doctorCheck `json:"checks"`
}

const (
	doctorSectionCore           = "Core"
	doctorSectionTools          = "Tools"
	doctorSectionAgents         = "Agent harnesses"
	doctorSectionGitHub         = "GitHub"
	doctorSectionGitLab         = "GitLab"
	minGitVersion               = "2.25.0"
	githubDoctorUserAgent       = "ao-agent-orchestrator/doctor"
	gitlabDoctorUserAgent       = "ao-agent-orchestrator/doctor"
	defaultDoctorGitHubRESTBase = "https://api.github.com"
	defaultDoctorGitLabRESTBase = "https://gitlab.com/api/v4"
)

type harnessProbe struct {
	Name                  string
	BinaryName            string
	VersionArg            string
	ExpectedVersionPrefix string
}

type harnessProbeSpec struct {
	BinaryName            string
	VersionArg            string
	ExpectedVersionPrefix string
}

var harnessProbeSpecs = map[string]harnessProbeSpec{
	"claude-code":  {BinaryName: "claude", VersionArg: "--version"},
	"codex":        {BinaryName: "codex", VersionArg: "--version"},
	"opencode":     {BinaryName: "opencode", VersionArg: "--version"},
	"muse":         {BinaryName: "muse", VersionArg: "--version", ExpectedVersionPrefix: "Muse Code "},
	"aider":        {BinaryName: "aider", VersionArg: "--version"},
	"goose":        {BinaryName: "goose", VersionArg: "--version"},
	"grok":         {BinaryName: "grok", VersionArg: "--version"},
	"devin":        {BinaryName: "devin", VersionArg: "--version"},
	"kimi":         {BinaryName: "kimi", VersionArg: "--version"},
	"kiro":         {BinaryName: "kiro-cli", VersionArg: "--version"},
	"kilocode":     {BinaryName: "kilocode", VersionArg: "--version"},
	"omp":          {BinaryName: "omp", VersionArg: "--version"},
	"codewhale":    {BinaryName: "codewhale", VersionArg: "--version"},
	"cursor":       {BinaryName: "cursor-agent"},
	"continue":     {BinaryName: "cn"},
	"memcode":      {BinaryName: "memcode", VersionArg: "--version"},
	"command-code": {BinaryName: "command-code", VersionArg: "--version"},
	"hermes":       {BinaryName: "hermes", VersionArg: "--version"},
	"stakpak":      {BinaryName: "stakpak", VersionArg: "--version"},
	// MiniMax Code's CLI is `mcode`, not its harness ID.
	"minimax-code": {BinaryName: "mcode", VersionArg: "--version"},
}

func newDoctorCommand(ctx *commandContext) *cobra.Command {
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "doctor",
		Short: "Run local Orchestrator health checks",
		Args:  noArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			checks := ctx.runDoctor(cmd.Context())
			failures := 0
			for _, check := range checks {
				if check.Level == doctorFail {
					failures++
				}
			}

			if asJSON {
				if err := writeJSON(cmd.OutOrStdout(), doctorReport{
					OK: failures == 0, Failures: failures, Checks: checks,
				}); err != nil {
					return err
				}
			} else {
				if err := writeDoctorText(cmd, checks); err != nil {
					return err
				}
			}

			if failures > 0 {
				return fmt.Errorf("doctor found %d failing check(s)", failures)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output health checks as JSON")
	return cmd
}

func writeDoctorText(cmd *cobra.Command, checks []doctorCheck) error {
	var lastSection string
	for _, check := range checks {
		if check.Section != "" && check.Section != lastSection {
			if lastSection != "" {
				if _, err := fmt.Fprintln(cmd.OutOrStdout()); err != nil {
					return err
				}
			}
			if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", check.Section); err != nil {
				return err
			}
			lastSection = check.Section
		}
		if _, err := fmt.Fprintf(cmd.OutOrStdout(), "%s %s: %s\n", check.Level, check.Name, check.Message); err != nil {
			return err
		}
	}
	return nil
}

func (c *commandContext) runDoctor(ctx context.Context) []doctorCheck {
	checks := []doctorCheck{}

	cfg, err := config.Load()
	if err != nil {
		return append(checks, doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "config", Message: err.Error()})
	}
	checks = append(checks, doctorCheck{
		Level: doctorPass, Section: doctorSectionCore, Name: "config",
		Message: fmt.Sprintf("runFile=%s dataDir=%s port=%d", cfg.RunFilePath, cfg.DataDir, cfg.Port),
	})

	if err := os.MkdirAll(cfg.DataDir, 0o750); err != nil {
		checks = append(checks, doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir", Message: err.Error()})
	} else {
		checks = append(checks,
			doctorCheck{Level: doctorPass, Section: doctorSectionCore, Name: "data-dir", Message: cfg.DataDir},
			checkDataDirWritable(cfg.DataDir),
		)
	}

	checks = append(checks, checkStore(cfg.DataDir), checkHooksLog(cfg.DataDir, time.Now()))

	daemonExe := ""
	st, err := c.inspectDaemon(ctx)
	if err != nil {
		checks = append(checks, doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "daemon", Message: err.Error()})
	} else {
		daemonExe = st.ExecutablePath
		level := doctorPass
		switch st.State {
		case stateStale, stateNotReady:
			level = doctorWarn
		case stateUnhealthy:
			level = doctorFail
		}
		msg := string(st.State)
		if st.PID != 0 {
			msg = fmt.Sprintf("%s pid=%d port=%d", msg, st.PID, st.Port)
		}
		if st.Error != "" {
			msg += " (" + st.Error + ")"
		}
		checks = append(checks, doctorCheck{Level: level, Section: doctorSectionCore, Name: "daemon", Message: msg})
	}

	checks = append(checks,
		c.checkGit(ctx),
		c.checkTerminalRuntime(ctx),
		c.checkAOBinary(daemonExe),
	)
	if check, ok := c.checkAOLoginShell(ctx, daemonExe, cfg.DataDir); ok {
		checks = append(checks, check)
	}
	for _, ha := range registry.Harnessed() {
		id := string(ha.Harness)
		spec := harnessProbeSpecs[id]
		binaryName := spec.BinaryName
		if binaryName == "" {
			binaryName = id
		}
		checks = append(checks, c.checkHarness(ctx, harnessProbe{
			Name:                  id,
			BinaryName:            binaryName,
			VersionArg:            spec.VersionArg,
			ExpectedVersionPrefix: spec.ExpectedVersionPrefix,
		}))
	}
	checks = append(checks, c.checkCodexLaunchFlags(ctx), c.checkGitHubToken(ctx), c.checkGitLabToken(ctx))
	return checks
}

func checkStore(dataDir string) doctorCheck {
	dbPath := filepath.Join(dataDir, "ao.db")
	info, err := os.Stat(dbPath)
	switch {
	case err == nil:
		return doctorCheck{
			Level: doctorPass, Section: doctorSectionCore, Name: "sqlite",
			Message: fmt.Sprintf("%s (%d bytes); migrations are applied by the daemon at startup", dbPath, info.Size()),
		}
	case errors.Is(err, fs.ErrNotExist):
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionCore, Name: "sqlite",
			Message: "database not created yet; run `ao start` to initialize and migrate it",
		}
	default:
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "sqlite", Message: err.Error()}
	}
}

func checkDataDirWritable(dataDir string) doctorCheck {
	f, err := os.CreateTemp(dataDir, ".ao-doctor-write-*")
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir-write", Message: err.Error()}
	}
	name := f.Name()
	if _, err := f.WriteString("ok\n"); err != nil {
		_ = f.Close()
		_ = os.Remove(name)
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir-write", Message: err.Error()}
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return doctorCheck{Level: doctorFail, Section: doctorSectionCore, Name: "data-dir-write", Message: err.Error()}
	}
	if err := os.Remove(name); err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionCore, Name: "data-dir-write", Message: fmt.Sprintf("write probe succeeded but cleanup failed: %v", err)}
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionCore, Name: "data-dir-write", Message: "write probe succeeded"}
}

func (c *commandContext) checkAOBinary(daemonExe string) doctorCheck {
	const name = "ao-binary"
	self, err := c.deps.Executable()
	if err != nil && daemonExe == "" {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: name, Message: fmt.Sprintf("could not resolve the running executable: %v", err)}
	}
	want, wantLabel := self, "this binary"
	if daemonExe != "" {
		want, wantLabel = daemonExe, "the running daemon's binary"
	}
	onPath, err := c.deps.LookPath("ao")
	if err != nil || onPath == "" {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionTools, Name: name,
			Message: "ao not found in PATH; workspace hooks invoke `ao hooks <agent> <event>` (daemon-spawned sessions pin PATH to the daemon binary and are unaffected)",
		}
	}
	if sameBinary(want, onPath) {
		return doctorCheck{Level: doctorPass, Section: doctorSectionTools, Name: name, Message: fmt.Sprintf("ao in PATH is %s (%s)", wantLabel, onPath)}
	}
	if daemonExe != "" {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionTools, Name: name,
			Message: fmt.Sprintf("ao in PATH is %s, which shadows the running daemon's binary %s; remove or reorder the shadowing install so `ao` outside daemon-spawned sessions is the one the app runs", onPath, daemonExe),
		}
	}
	return doctorCheck{
		Level: doctorWarn, Section: doctorSectionTools, Name: name,
		Message: fmt.Sprintf("ao in PATH is %s, not this binary (%s); workspace hooks run `ao hooks` and a foreign ao breaks activity tracking outside daemon-spawned sessions", onPath, self),
	}
}

func sameBinary(a, b string) bool {
	ai, aErr := os.Stat(a)
	bi, bErr := os.Stat(b)
	if aErr == nil && bErr == nil {
		return os.SameFile(ai, bi)
	}
	return filepath.Clean(a) == filepath.Clean(b)
}

const loginShellProbeTimeout = 3 * time.Second

func (c *commandContext) checkAOLoginShell(ctx context.Context, daemonExe, dataDir string) (check doctorCheck, ok bool) {
	const name = "ao-login-shell"
	if runtime.GOOS == "windows" {
		return doctorCheck{}, false
	}
	want, wantLabel := daemonExe, "the running daemon's binary"
	if want == "" {
		self, err := c.deps.Executable()
		if err != nil {
			return doctorCheck{}, false
		}
		want, wantLabel = self, "this binary"
	}
	shell := strings.TrimSpace(os.Getenv("SHELL"))
	shellName := filepath.Base(shell)
	switch shellName {
	case "sh", "bash", "zsh", "dash", "ksh":
	default:
		return doctorCheck{}, false
	}
	if !filepath.IsAbs(shell) || filepath.Base(want) != "ao" {
		return doctorCheck{}, false
	}

	couldNotRun := func(err error) (doctorCheck, bool) {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionTools, Name: name,
			Message: fmt.Sprintf("could not run a %s login shell to see which ao it runs: %v", shellName, err),
		}, true
	}
	dir, err := os.MkdirTemp("", "ao-doctor-login-shell-*")
	if err != nil {
		return couldNotRun(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	answerPath := filepath.Join(dir, "ao")
	pinned := agentlaunch.PinnedDir(func() (string, error) { return want, nil }, dataDir)
	if pinned == "" {
		pinned = filepath.Dir(want)
	}
	path := pinned
	if current := os.Getenv("PATH"); current != "" {
		path += string(os.PathListSeparator) + current
	}
	loginEnv, err := agentlaunch.LoginShellEnv(path, os.Getenv, nil, dataDir)
	if err != nil {
		return couldNotRun(err)
	}
	args := []string{"-c", `exec "$@" </dev/null >/dev/null 2>&1`, "sh", "/usr/bin/env", "PATH=" + path}
	for _, key := range slices.Sorted(maps.Keys(loginEnv)) {
		args = append(args, key+"="+loginEnv[key])
	}
	args = append(args, shell, "-l", "-c", `command -v ao >"$1"`, shellName, answerPath)
	reqCtx, cancel := context.WithTimeout(ctx, loginShellProbeTimeout)
	defer cancel()
	_, err = c.deps.CommandOutput(reqCtx, "/bin/sh", args...)
	answer, readErr := os.ReadFile(answerPath) //nolint:gosec // path is inside doctor's own temp dir
	resolved := strings.TrimSpace(string(answer))
	timedOut := errors.Is(reqCtx.Err(), context.DeadlineExceeded)
	if readErr != nil || (resolved == "" && timedOut) {
		switch {
		case timedOut:
			err = fmt.Errorf("timed out after %s", loginShellProbeTimeout)
		case err == nil:
			err = errors.New("a startup file ended the shell before it checked")
		}
		return couldNotRun(err)
	}

	var msg string
	switch {
	case resolved == "":
		msg = fmt.Sprintf("a %s login shell finds no ao even with %s first on PATH: a startup file replaces PATH, so agent commands that run in a login shell (Codex's) and new terminals cannot run ao", shellName, pinned)
	case !filepath.IsAbs(resolved):
		msg = fmt.Sprintf("a %s login shell resolves ao to %q, not %s %s: an alias or function from a startup file shadows it in agent commands that run in a login shell (Codex's) and new terminals", shellName, resolved, wantLabel, want)
	case sameBinary(want, resolved) || (pinned != filepath.Dir(want) && sameBinary(filepath.Join(pinned, "ao"), resolved)):
		return doctorCheck{
			Level: doctorPass, Section: doctorSectionTools, Name: name,
			Message: fmt.Sprintf("a %s login shell still runs %s (%s)", shellName, wantLabel, want),
		}, true
	default:
		msg = fmt.Sprintf("a %s login shell runs %s, not %s %s: a startup file such as ~/.profile puts its directory first, and Orchestrator puts its own ao back only in zsh and bash, so agent commands that run in a login shell (Codex's) and new terminals run that copy; remove it or stop prepending its directory", shellName, resolved, wantLabel, want)
		if agentlaunch.IsDevLauncher(resolved, agentlaunch.DevLauncherBuildPaths(os.Getenv)) {
			msg += fmt.Sprintf("; it is the launcher scripts/daemon-build.sh installs, and `rm '%s'` removes it", strings.ReplaceAll(resolved, "'", `'\''`))
		}
	}
	return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: name, Message: msg}, true
}

func (c *commandContext) checkGit(ctx context.Context) doctorCheck {
	path, err := c.deps.LookPath("git")
	if err != nil || path == "" {
		return doctorCheck{Level: doctorFail, Section: doctorSectionTools, Name: "git", Message: "not found in PATH"}
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := c.deps.CommandOutput(reqCtx, path, "--version")
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s: %v", path, err)}
	}
	version, err := parseGitVersion(string(out))
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version unknown: %s)", path, firstOutputLine(out))}
	}
	cmp, err := compareDottedVersion(version, minGitVersion)
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version unknown: %s)", path, firstOutputLine(out))}
	}
	if cmp < 0 {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version %s; Orchestrator expects >= %s for worktrees)", path, version, minGitVersion)}
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionTools, Name: "git", Message: fmt.Sprintf("%s (version %s; supports worktrees)", path, version)}
}

func (c *commandContext) checkTerminalRuntime(ctx context.Context) doctorCheck {
	if runtime.GOOS == "windows" {
		return doctorCheck{
			Level:   doctorPass,
			Section: doctorSectionTools,
			Name:    "conpty",
			Message: "ConPTY (built-in): no external terminal multiplexer required on Windows",
		}
	}
	return c.checkTmux(ctx)
}

func (c *commandContext) checkTmux(ctx context.Context) doctorCheck {
	resolution, err := tmuxbin.ResolveWith(os.Getenv("AO_TMUX_BINARY"), c.deps.Executable, c.deps.LookPath)
	if err != nil || resolution.Path == "" {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionTools, Name: "tmux", Message: "no configured, bundled, or system tmux found for this ao process; required on macOS/Linux to start sessions"}
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := c.deps.CommandOutput(reqCtx, resolution.Path, "-V")
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionTools, Name: "tmux", Message: fmt.Sprintf("%s (%s for this ao process): %v", resolution.Path, resolution.Source, err)}
	}
	version := firstOutputLine(out)
	if version == "" {
		version = "version unknown"
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionTools, Name: "tmux", Message: fmt.Sprintf("%s (%s for this ao process; %s)", resolution.Path, resolution.Source, version)}
}

func checkHooksLog(dataDir string, now time.Time) doctorCheck {
	const name = "hooks-log"
	path := filepath.Join(dataDir, hooksLogName)
	data, err := os.ReadFile(path) //nolint:gosec // path rooted in AO's own data dir
	if errors.Is(err, fs.ErrNotExist) {
		return doctorCheck{Level: doctorPass, Section: doctorSectionCore, Name: name, Message: "no hook delivery failures recorded"}
	}
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionCore, Name: name, Message: err.Error()}
	}

	recent := 0
	latest := ""
	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		stamp, _, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		ts, err := time.Parse(time.RFC3339, stamp)
		if err != nil || now.Sub(ts) > 24*time.Hour {
			continue
		}
		recent++
		latest = line
	}
	if recent == 0 {
		return doctorCheck{Level: doctorPass, Section: doctorSectionCore, Name: name, Message: fmt.Sprintf("no hook delivery failures in the last 24h (%s)", path)}
	}
	return doctorCheck{
		Level: doctorWarn, Section: doctorSectionCore, Name: name,
		Message: fmt.Sprintf("%d hook delivery failure(s) in the last 24h — activity tracking may be degraded; latest: %s (full log: %s)", recent, latest, path),
	}
}

func (c *commandContext) checkHarness(ctx context.Context, harness harnessProbe) doctorCheck {
	path, err := c.deps.LookPath(harness.BinaryName)
	if err != nil || path == "" {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionAgents, Name: harness.Name,
			Message: fmt.Sprintf("%s not found in PATH", harness.BinaryName),
		}
	}
	if harness.VersionArg == "" {
		return doctorCheck{Level: doctorPass, Section: doctorSectionAgents, Name: harness.Name, Message: fmt.Sprintf("%s resolves to %s", harness.BinaryName, path)}
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, err := c.deps.CommandOutput(reqCtx, path, harness.VersionArg)
	if err != nil {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionAgents, Name: harness.Name,
			Message: fmt.Sprintf("%s resolves to %s, but `%s %s` failed: %v", harness.BinaryName, path, harness.BinaryName, harness.VersionArg, err),
		}
	}
	version := firstOutputLine(out)
	if version == "" {
		version = "version output was empty"
	}
	if harness.ExpectedVersionPrefix != "" && !strings.HasPrefix(version, harness.ExpectedVersionPrefix) {
		return doctorCheck{
			Level: doctorWarn, Section: doctorSectionAgents, Name: harness.Name,
			Message: fmt.Sprintf("%s resolves to %s, but its version output %q does not identify the expected CLI (%q prefix)", harness.BinaryName, path, version, harness.ExpectedVersionPrefix),
		}
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionAgents, Name: harness.Name, Message: fmt.Sprintf("%s resolves to %s (%s)", harness.BinaryName, path, version)}
}

func (c *commandContext) checkCodexLaunchFlags(ctx context.Context) doctorCheck {
	const name = "codex-launch-flags"
	path, err := c.deps.LookPath("codex")
	if err != nil || path == "" {
		return doctorCheck{Level: doctorPass, Section: doctorSectionAgents, Name: name, Message: "skipped: codex not found in PATH"}
	}
	for _, probe := range codex.DoctorLaunchProbes() {
		reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
		out, err := c.deps.CommandOutput(reqCtx, path, probe...)
		cancel()
		if err != nil {
			return doctorCheck{
				Level: doctorWarn, Section: doctorSectionAgents, Name: name,
				Message: fmt.Sprintf("codex rejected Orchestrator's launch flags (`codex %s`: %v) — codex sessions may spawn without activity hooks; a codex CLI update likely changed its flag/config surface", strings.Join(probe, " "), err),
			}
		}
		if strings.Contains(string(out), "unknown configuration field") {
			return doctorCheck{
				Level: doctorWarn, Section: doctorSectionAgents, Name: name,
				Message: fmt.Sprintf("codex no longer recognizes one of Orchestrator's config overrides (%s) — codex sessions may spawn without activity hooks", firstOutputLine(out)),
			}
		}
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionAgents, Name: name, Message: "codex accepts Orchestrator's hook/trust launch flags"}
}

func (c *commandContext) checkGitHubToken(ctx context.Context) doctorCheck {
	token, source, err := c.githubToken(ctx)
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionGitHub, Name: "github-token", Message: err.Error()}
	}

	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(c.deps.DoctorGitHubRESTBase, "/")+"/user", http.NoBody)
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: err.Error()}
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", githubDoctorUserAgent)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token validation failed: %v", source, err)}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token rejected by GitHub (HTTP %d)", source, resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token probe returned HTTP %d", source, resp.StatusCode)}
	}

	var user struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token probe decode failed: %v", source, err)}
	}
	login := user.Login
	if login == "" {
		login = "unknown user"
	}
	scopes := strings.TrimSpace(resp.Header.Get("X-OAuth-Scopes"))
	scopeMsg := "scopes unavailable"
	if scopes != "" {
		scopeMsg = "scopes: " + scopes
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionGitHub, Name: "github-token", Message: fmt.Sprintf("%s token valid for %s (%s)", source, login, scopeMsg)}
}

func (c *commandContext) githubToken(ctx context.Context) (token, source string, err error) {
	for _, name := range []string{"AO_GITHUB_TOKEN", "GITHUB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, name, nil
		}
	}
	path, lookErr := c.deps.LookPath("gh")
	if lookErr != nil || path == "" {
		return "", "", errors.New("no GitHub token found (set AO_GITHUB_TOKEN/GITHUB_TOKEN or run `gh auth login`)")
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, cmdErr := c.deps.CommandOutput(reqCtx, path, "auth", "token")
	if cmdErr != nil {
		return "", "", fmt.Errorf("gh is installed but no token was available (`gh auth token` failed: %w)", cmdErr)
	}
	token = strings.TrimSpace(string(out))
	if token == "" {
		return "", "", errors.New("gh is installed but returned an empty auth token")
	}
	return token, "gh", nil
}

func (c *commandContext) checkGitLabToken(ctx context.Context) doctorCheck {
	token, source, err := c.gitlabToken(ctx)
	if err != nil {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionGitLab, Name: "gitlab-token", Message: err.Error()}
	}

	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, http.MethodGet, strings.TrimRight(c.deps.DoctorGitLabRESTBase, "/")+"/user", http.NoBody)
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitLab, Name: "gitlab-token", Message: err.Error()}
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", gitlabDoctorUserAgent)
	req.Header.Set("PRIVATE-TOKEN", token)
	resp, err := c.deps.HTTPClient.Do(req)
	if err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitLab, Name: "gitlab-token", Message: fmt.Sprintf("%s token validation failed: %v", source, err)}
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitLab, Name: "gitlab-token", Message: fmt.Sprintf("%s token rejected by GitLab (HTTP %d)", source, resp.StatusCode)}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return doctorCheck{Level: doctorWarn, Section: doctorSectionGitLab, Name: "gitlab-token", Message: fmt.Sprintf("%s token probe returned HTTP %d", source, resp.StatusCode)}
	}

	var user struct {
		Username string `json:"username"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return doctorCheck{Level: doctorFail, Section: doctorSectionGitLab, Name: "gitlab-token", Message: fmt.Sprintf("%s token probe decode failed: %v", source, err)}
	}
	login := user.Username
	if login == "" {
		login = "unknown user"
	}
	return doctorCheck{Level: doctorPass, Section: doctorSectionGitLab, Name: "gitlab-token", Message: fmt.Sprintf("%s token valid for %s", source, login)}
}

func (c *commandContext) gitlabToken(ctx context.Context) (token, source string, err error) {
	for _, name := range []string{"AO_GITLAB_TOKEN", "GITLAB_TOKEN"} {
		if v := strings.TrimSpace(os.Getenv(name)); v != "" {
			return v, name, nil
		}
	}
	path, lookErr := c.deps.LookPath("glab")
	if lookErr != nil || path == "" {
		return "", "", errors.New("no GitLab token found (set AO_GITLAB_TOKEN/GITLAB_TOKEN or run `glab auth login`)")
	}
	reqCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	out, cmdErr := c.deps.CommandOutput(reqCtx, path, "auth", "status", "--show-token")
	if cmdErr != nil {
		return "", "", fmt.Errorf("glab is installed but no token was available (`glab auth status --show-token` failed: %w)", cmdErr)
	}
	token = parseGLabTokenLine(string(out))
	if token == "" {
		return "", "", errors.New("glab is installed but returned no auth token")
	}
	return token, "glab", nil
}

func parseGLabTokenLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		tokenIdx := strings.Index(line, "Token")
		if tokenIdx < 0 {
			continue
		}
		colonIdx := strings.Index(line[tokenIdx:], ":")
		if colonIdx < 0 {
			continue
		}
		val := strings.TrimSpace(line[tokenIdx+colonIdx+1:])
		if val != "" {
			return val
		}
	}
	return ""
}

var (
	ansiRE       = regexp.MustCompile(`\x1b\[[0-9;]*[A-Za-z]`)
	gitVersionRE = regexp.MustCompile(`(?i)\bgit version\s+(\d+(?:\.\d+){1,3})`)
)

func parseGitVersion(out string) (string, error) {
	clean := ansiRE.ReplaceAllString(out, "")
	m := gitVersionRE.FindStringSubmatch(clean)
	if len(m) < 2 {
		return "", fmt.Errorf("parse git version from %q", strings.TrimSpace(clean))
	}
	return m[1], nil
}

func firstOutputLine(out []byte) string {
	clean := strings.TrimSpace(ansiRE.ReplaceAllString(string(out), ""))
	if clean == "" {
		return ""
	}
	line := strings.SplitN(clean, "\n", 2)[0]
	return strings.TrimSpace(line)
}

func compareDottedVersion(a, b string) (int, error) {
	ap, err := dottedVersionParts(a)
	if err != nil {
		return 0, err
	}
	bp, err := dottedVersionParts(b)
	if err != nil {
		return 0, err
	}
	maxLen := len(ap)
	if len(bp) > maxLen {
		maxLen = len(bp)
	}
	for i := 0; i < maxLen; i++ {
		var av, bv int
		if i < len(ap) {
			av = ap[i]
		}
		if i < len(bp) {
			bv = bp[i]
		}
		switch {
		case av < bv:
			return -1, nil
		case av > bv:
			return 1, nil
		}
	}
	return 0, nil
}

func dottedVersionParts(s string) ([]int, error) {
	raw := strings.Split(s, ".")
	parts := make([]int, 0, len(raw))
	for _, part := range raw {
		if part == "" {
			return nil, fmt.Errorf("empty version segment in %q", s)
		}
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, fmt.Errorf("parse version segment %q in %q: %w", part, s, err)
		}
		parts = append(parts, n)
	}
	return parts, nil
}
