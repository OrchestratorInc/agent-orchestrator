// Package agy defines AO's experimental host-trusted Antigravity reviewer.
package agy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	agentagy "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/agy"
	"github.com/aoagents/agent-orchestrator/backend/internal/agentlaunch"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
	"github.com/aoagents/agent-orchestrator/backend/internal/reviewgateway"
)

// HarnessID identifies the Agy reviewer adapter.
const HarnessID domain.ReviewerHarness = "agy"

const minimumVersion = "1.1.6"

// HostTrustWarning documents the authority retained by Agy's interactive TUI.
const HostTrustWarning = "experimental host-trusted reviewer: Agy sandbox mode is not OS isolation and its built-in tools retain host authority"

var requiredFlags = []string{"--agent", "--conversation", "--prompt-interactive", "--sandbox"}

// Reviewer describes Agy's host-trusted interactive lifecycle.
type Reviewer struct {
	resolveBinary func(context.Context) (string, error)
	run           func(context.Context, map[string]string, string, ...string) ([]byte, error)
}

// New returns the experimental host-trusted adapter.
func New() *Reviewer {
	return &Reviewer{
		resolveBinary: agentagy.ResolveAgyBinary,
		run: func(ctx context.Context, env map[string]string, binary string, args ...string) ([]byte, error) {
			cmd := aoprocess.CommandContext(ctx, binary, args...)
			cmd.Env = appendEnvironment(os.Environ(), env)
			return cmd.CombinedOutput()
		},
	}
}

// Harness identifies the Agy reviewer.
func (*Reviewer) Harness() domain.ReviewerHarness { return HarnessID }

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)

// ReviewCommand launches Agy's permanent interactive TUI in sandbox mode. The
// checkout and AO prompt root are explicitly added because this experimental
// mode is host-trusted rather than contained.
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	binary, err := r.resolveBinary(ctx)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	if strings.TrimSpace(inv.TaskPromptRoot) == "" {
		return ports.ReviewCommandSpec{Argv: []string{binary}}, nil
	}
	if !filepath.IsAbs(binary) {
		return ports.ReviewCommandSpec{}, errors.New("agy reviewer: resolved binary must be absolute")
	}
	env, err := reviewgateway.PrepareHostTrustedEnvironment(inv.DataDir, inv.ReviewerID)
	if err != nil {
		return ports.ReviewCommandSpec{}, fmt.Errorf("agy reviewer: prepare Orchestrator-owned profile: %w", err)
	}
	envVars := env.TUIEnvironment()
	envVars["AO_DATA_DIR"] = env.DataDir
	if err := seedOnboardingComplete(env.ConfigRoot); err != nil {
		return ports.ReviewCommandSpec{}, fmt.Errorf("agy reviewer: seed first-run state: %w", err)
	}
	if err := seedTrustedWorkspace(env.ConfigRoot, inv.WorkspacePath); err != nil {
		return ports.ReviewCommandSpec{}, fmt.Errorf("agy reviewer: seed workspace trust: %w", err)
	}
	agentagy.ApplyAuthModeEnv(envVars, inv.DataDir)
	if err := r.verifyCompatibility(ctx, binary, envVars); err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	// Probe with the pane's environment: project env under the reviewer roots,
	// as the launcher merges it, so project-level gateway settings count.
	if err := r.verifySignedIn(ctx, binary, env.ConfigRoot, agentlaunch.MergeEnv(inv.ProjectEnv, envVars)); err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	prompt := inv.Prompt
	if strings.TrimSpace(inv.SystemPromptFile) != "" {
		prompt = "Read and follow the reviewer system instructions at `" + filepath.ToSlash(inv.SystemPromptFile) + "`, then " + prompt
	}
	return ports.ReviewCommandSpec{
		Argv:             interactiveArgv(binary, inv.WorkspacePath, inv.TaskPromptRoot, inv.Config.Model, prompt),
		Env:              envVars,
		WorkingDirectory: inv.WorkspacePath,
	}, nil
}

// ReviewRestoreCommand restores a recorded Agy reviewer pane by relaunching
// the reviewer command with its AO-owned profile and current task context.
func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, err := r.ReviewCommand(ctx, inv)
	return cmd, true, err
}

// ReviewPreflight verifies that the Agy executable is available. Compatibility
// probing happens after AO-owned state roots are available in ReviewCommand.
func (r *Reviewer) ReviewPreflight(ctx context.Context, _ string) error {
	_, err := r.resolveBinary(ctx)
	return err
}

func (r *Reviewer) verifyCompatibility(ctx context.Context, binary string, env map[string]string) error {
	help, err := r.run(ctx, env, binary, "--help")
	if err != nil {
		return fmt.Errorf("run agy --help: %w", err)
	}
	for _, flag := range requiredFlags {
		if !strings.Contains(string(help), flag) {
			return fmt.Errorf("installed Agy is incompatible: required flag %s is unavailable", flag)
		}
	}
	version, err := r.run(ctx, env, binary, "--version")
	if err != nil {
		return fmt.Errorf("run agy --version: %w", err)
	}
	if !versionAtLeast(strings.TrimSpace(string(version)), minimumVersion) {
		return fmt.Errorf("installed Agy %q is incompatible: version %s or newer is required", strings.TrimSpace(string(version)), minimumVersion)
	}
	return nil
}

// signInProbeTimeout bounds the `agy models` sign-in probe.
const signInProbeTimeout = 20 * time.Second

// verifySignedIn fails fast when Agy is not signed in inside the isolated
// reviewer home. A signed-out Agy opens its login picker, which nobody can
// answer in the reviewer pane, so the review would wait forever (#8862).
//
// `agy models` starts no login. It exits 0 even when signed out, so only its
// sign-in message counts as signed out; any other result, including a probe
// error, is inconclusive and the launch proceeds as before.
func (r *Reviewer) verifySignedIn(ctx context.Context, binary, configRoot string, env map[string]string) error {
	probeCtx, cancel := context.WithTimeout(ctx, signInProbeTimeout)
	defer cancel()
	out, _ := r.run(probeCtx, env, binary, "models")
	if !strings.Contains(strings.ToLower(string(out)), "please sign in") {
		return nil
	}
	home := shellQuote(configRoot)
	return &ports.ReviewerAuthRequiredError{Guidance: fmt.Sprintf("Agy is not signed in for reviews. Sign in in this worker session's reviewer home by running `HOME=%s XDG_CONFIG_HOME=%s %s` and choosing a login method, then retry the review. The sign-in applies to this worker session's reviewer only.", home, home, shellQuote(binary))}
}

// shellQuote single-quotes a value for a POSIX shell, so paths with spaces or
// `$` are pasted back literally rather than expanded.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func appendEnvironment(base []string, overrides map[string]string) []string {
	result := append([]string(nil), base...)
	for key, value := range overrides {
		result = append(result, key+"="+value)
	}
	return result
}

// ReviewMessage returns the short AO-owned task-file reference for a future
// long-lived TUI. The full instructions remain outside terminal scrollback.
func (*Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

// ReviewCancel matches Agy's fixed TUI behavior: the first Ctrl-C interrupts
// the active operation, while a second Ctrl-C begins the exit flow.
func (*Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{Mode: ports.ReviewCancelInterrupt, Interrupts: 1}, nil
}

// onboardingFlags are the cache/onboarding.json keys Agy's TUI reads to decide
// whether to show its first-run setup wizard. Consumer and enterprise sign-ins
// (e.g. an AGY_LLM_GATEWAY_URL gateway) each check their own flag, so all are
// set.
var onboardingFlags = []string{"consumerOnboardingComplete", "enterpriseOnboardingComplete", "onboardingComplete"}

// seedOnboardingComplete marks Agy's first-run setup as done inside the
// isolated reviewer home. Without it the reviewer TUI opens on the setup
// wizard (theme picker), which nobody can answer, so no verdict is returned
// (#8862). Existing keys written by Agy are preserved.
func seedOnboardingComplete(configRoot string) error {
	dir := filepath.Join(configRoot, ".gemini", agentagy.CLIStorageDir, "cache")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "onboarding.json")
	// Readable state is preserved; missing, unreadable, or corrupt state is
	// replaced so it can never block the reviewer launch.
	state := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &state) != nil || state == nil {
			state = map[string]json.RawMessage{}
		}
	}
	for _, key := range onboardingFlags {
		state[key] = json.RawMessage("true")
	}
	return writePrivateJSON(path, state)
}

// trustedWorkspacesKey is the settings.json list of folders Agy trusts. Agy
// asks "Do you trust the contents of this project?" for an untrusted working
// directory, which nobody can answer in the reviewer pane (#8862).
const trustedWorkspacesKey = "trustedWorkspaces"

// seedTrustedWorkspace adds the reviewer's working directory to Agy's trusted
// folders inside the isolated reviewer home only. It trusts exactly that
// workspace (as given and, if different, with symlinks resolved, since Agy may
// see either form), never an ancestor or the home directory. The --add-dir
// prompt root needs no trust entry. Other settings and existing entries are
// kept; missing, unreadable, or corrupt settings are replaced.
func seedTrustedWorkspace(configRoot, workspacePath string) error {
	workspacePath = strings.TrimSpace(workspacePath)
	if workspacePath == "" || !filepath.IsAbs(workspacePath) {
		return nil
	}
	candidates := []string{filepath.Clean(workspacePath)}
	if resolved, err := filepath.EvalSymlinks(workspacePath); err == nil && resolved != candidates[0] {
		candidates = append(candidates, resolved)
	}
	dir := filepath.Join(configRoot, ".gemini", agentagy.CLIStorageDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := filepath.Join(dir, "settings.json")
	settings := map[string]json.RawMessage{}
	if raw, err := os.ReadFile(path); err == nil {
		if json.Unmarshal(raw, &settings) != nil || settings == nil {
			settings = map[string]json.RawMessage{}
		}
	}
	var trusted []json.RawMessage
	if raw, ok := settings[trustedWorkspacesKey]; ok && json.Unmarshal(raw, &trusted) != nil {
		trusted = nil
	}
	present := map[string]bool{}
	for _, entry := range trusted {
		var value string
		if json.Unmarshal(entry, &value) == nil {
			present[value] = true
		}
	}
	for _, candidate := range candidates {
		if present[candidate] {
			continue
		}
		entry, err := json.Marshal(candidate)
		if err != nil {
			return err
		}
		trusted = append(trusted, entry)
		present[candidate] = true
	}
	raw, err := json.Marshal(trusted)
	if err != nil {
		return err
	}
	settings[trustedWorkspacesKey] = raw
	return writePrivateJSON(path, settings)
}

// writePrivateJSON writes value through a private temp file renamed into
// place, so an existing file with an unusable mode is replaced rather than
// reused. Directory and storage failures surface as errors.
func writePrivateJSON(path string, value any) error {
	raw, err := json.Marshal(value)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+strings.TrimSuffix(filepath.Base(path), ".json")+"-*.json")
	if err != nil {
		return err
	}
	// Best-effort cleanup; after a successful rename the temp name is gone.
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func interactiveArgv(binary, workspacePath, promptRoot, model, prompt string) []string {
	argv := []string{binary, "--sandbox"}
	// Mirror the Agy worker launcher: pass the configured model, and omit the
	// flag when unset so Agy falls back to its own default (#8919).
	if model = strings.TrimSpace(model); model != "" {
		argv = append(argv, "--model", model)
	}
	if strings.TrimSpace(workspacePath) != "" {
		argv = append(argv, "--add-dir", workspacePath)
	}
	if strings.TrimSpace(promptRoot) != "" {
		argv = append(argv, "--add-dir", promptRoot)
	}
	return append(argv, "--prompt-interactive", prompt)
}

func versionAtLeast(got, minimum string) bool {
	parse := func(value string) ([3]int, bool) {
		var result [3]int
		parts := strings.Split(strings.TrimPrefix(value, "v"), ".")
		if len(parts) < 3 {
			return result, false
		}
		for i := range result {
			n, err := strconv.Atoi(parts[i])
			if err != nil {
				return result, false
			}
			result[i] = n
		}
		return result, true
	}
	a, okA := parse(got)
	b, okB := parse(minimum)
	if !okA || !okB {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return true
}
