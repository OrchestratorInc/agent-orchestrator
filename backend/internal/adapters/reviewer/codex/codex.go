// Package codex adapts the codex worker agent for code-review sessions.
package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	workeragent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/reviewer/agentrestore"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Reviewer is the codex code-review adapter.
type Reviewer struct {
	agent ports.Agent
	// resolveBinary and runSandbox back ReviewPreflight's sandbox probe; nil
	// means the installed Codex.
	resolveBinary func(context.Context) (string, error)
	runSandbox    func(ctx context.Context, binary string, args ...string) ([]byte, error)
	// executable resolves the AO binary the review-guard hook runs; nil means
	// os.Executable (the daemon is the ao binary).
	executable func() (string, error)
}

// New builds the codex reviewer adapter.
func New() *Reviewer {
	return &Reviewer{agent: workeragent.New()}
}

// Harness identifies this reviewer in the reviewer registry.
func (r *Reviewer) Harness() domain.ReviewerHarness {
	return domain.ReviewerCodex
}

// ReviewEnforcement reports how the reviewer policy is enforced: Codex's read-
// only sandbox runs with approvals set to never, so no request can lift it.
func (*Reviewer) ReviewEnforcement() domain.ReviewerEnforcement {
	return domain.ReviewerEnforcedSandbox
}

// InvalidateBinaryResolution forgets the worker plugin's cached executable, so
// the next review runs the release an install just switched to.
func (r *Reviewer) InvalidateBinaryResolution() {
	if invalidator, ok := r.agent.(ports.AgentBinaryResolutionInvalidator); ok {
		invalidator.InvalidateBinaryResolution()
	}
}

var _ ports.Reviewer = (*Reviewer)(nil)
var _ ports.ReviewerCanceller = (*Reviewer)(nil)
var _ ports.ReviewerRestorer = (*Reviewer)(nil)
var _ ports.ReviewerChatProfile = (*Reviewer)(nil)
var _ interface {
	ReviewPreflight(context.Context, string) error
} = (*Reviewer)(nil)

// ReviewChatHarness selects Codex's typed app-server driver for review runs.
func (r *Reviewer) ReviewChatHarness() domain.AgentHarness { return domain.HarnessCodex }

// ReviewCommand launches the reviewer with an enforced read-only filesystem
// sandbox that has network access, approvals set to never (any approval path,
// including Codex's own auto-reviewer, could lift the sandbox), and the
// review-guard PreToolUse hook. The sandbox blocks every local write, however
// it is attempted; the hook blocks what the sandbox cannot see because it goes
// over the network, such as `git push` to a remote, `gh pr merge` or `curl`
// (#6842).
func (r *Reviewer) ReviewCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, error) {
	argv, err := r.agent.GetLaunchCommand(ctx, ports.LaunchConfig{
		Config:           inv.Config,
		SessionID:        inv.ReviewerID,
		WorkspacePath:    inv.WorkspacePath,
		Prompt:           inv.Prompt,
		SystemPrompt:     inv.SystemPrompt,
		SystemPromptFile: inv.SystemPromptFile,
		Permissions:      ports.PermissionModeAuto,
	})
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	argv, err = neverApprove(argv)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	extra, err := r.reviewerArgs(inv)
	if err != nil {
		return ports.ReviewCommandSpec{}, err
	}
	return ports.ReviewCommandSpec{Argv: insertBeforePrompt(argv, extra...)}, nil
}

// ReviewRestoreCommand resumes the reviewer Codex conversation captured from
// Codex hooks when AO recreates the reviewer pane after worker restore.
func (r *Reviewer) ReviewRestoreCommand(ctx context.Context, inv ports.ReviewInvocation) (ports.ReviewCommandSpec, bool, error) {
	cmd, ok, err := agentrestore.Command(ctx, r.agent, inv, agentrestore.Options{Permissions: ports.PermissionModeAuto})
	if err != nil || !ok {
		return cmd, ok, err
	}
	if cmd.Argv, err = neverApprove(cmd.Argv); err != nil {
		return ports.ReviewCommandSpec{}, false, err
	}
	extra, err := r.reviewerArgs(inv)
	if err != nil {
		return ports.ReviewCommandSpec{}, false, err
	}
	cmd.Argv = insertBeforeLastArg(cmd.Argv, extra...)
	return cmd, true, nil
}

// ReviewMessage returns the centrally-authored task for an existing pane.
func (r *Reviewer) ReviewMessage(_ context.Context, inv ports.ReviewInvocation) (string, error) {
	return inv.Prompt, nil
}

// ReviewCancel stops the active Codex reviewer turn while preserving the
// terminal pane for inspection. Codex advertises "esc to interrupt", and a
// single Escape stops the active turn without queuing prompt text.
func (r *Reviewer) ReviewCancel(context.Context) (ports.ReviewCancelSpec, error) {
	return ports.ReviewCancelSpec{
		Mode:  ports.ReviewCancelInput,
		Input: "\x1b",
	}, nil
}

func insertBeforePrompt(argv []string, extra ...string) []string {
	for i, arg := range argv {
		if arg == "--" {
			out := make([]string, 0, len(argv)+len(extra))
			out = append(out, argv[:i]...)
			out = append(out, extra...)
			return append(out, argv[i:]...)
		}
	}
	return append(argv, extra...)
}

func insertBeforeLastArg(argv []string, extra ...string) []string {
	if len(argv) == 0 {
		return append([]string{}, extra...)
	}
	out := make([]string, 0, len(argv)+len(extra))
	out = append(out, argv[:len(argv)-1]...)
	out = append(out, extra...)
	return append(out, argv[len(argv)-1])
}

// reviewerPermissionProfile is the Codex permission profile reviewers run
// under: the built-in read-only filesystem policy plus network, so `gh` and
// `ao review submit` can reach GitHub and the daemon without an approval
// prompt. Writes stay blocked by the OS sandbox, and the daemon's reviewer gate
// refuses session management from this network-enabled shell (#6842).
const reviewerPermissionProfile = "ao_reviewer"

// reviewerProfileArgs selects reviewerPermissionProfile. It replaces
// --sandbox read-only, which would override the profile and drop the network.
func reviewerProfileArgs() []string {
	return []string{
		"-c", `default_permissions="` + reviewerPermissionProfile + `"`,
		"-c", "permissions." + reviewerPermissionProfile + `.extends=":read-only"`,
		"-c", "permissions." + reviewerPermissionProfile + ".network.enabled=true",
	}
}

// ReviewPreflight refuses a Terminal reviewer when the installed Codex does
// not enforce reviewerPermissionProfile: a Codex that ignored the profile
// would fall back to a sandbox that can write the worktree. It runs two
// commands under the profile through `codex sandbox`: one that must succeed,
// proving the probe ran, and a write that the sandbox must block.
func (r *Reviewer) ReviewPreflight(ctx context.Context, _ string) error {
	resolve := r.resolveBinary
	if resolve == nil {
		resolve = workeragent.ResolveCodexBinary
	}
	binary, err := resolve(ctx)
	if err != nil {
		return err
	}
	run := r.runSandbox
	if run == nil {
		run = func(ctx context.Context, binary string, args ...string) ([]byte, error) {
			return aoprocess.CommandContext(ctx, binary, args...).Output()
		}
	}
	probeDir, err := os.MkdirTemp("", "ao-codex-reviewer-probe-")
	if err != nil {
		return fmt.Errorf("codex reviewer: create sandbox probe directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(probeDir) }()
	target := filepath.Join(probeDir, "write-probe")
	sandbox := func(script string) ([]byte, error) {
		args := append([]string{"sandbox"}, reviewerProfileArgs()...)
		if runtime.GOOS == "windows" {
			return run(ctx, binary, append(args, "--", "cmd", "/c", script)...)
		}
		return run(ctx, binary, append(args, "--", "sh", "-c", script)...)
	}
	if out, err := sandbox("echo ao-reviewer-probe"); err != nil || !strings.Contains(string(out), "ao-reviewer-probe") {
		return fmt.Errorf("codex reviewer: the installed Codex cannot run the read-only reviewer profile (%w); update Codex", err)
	}
	_, _ = sandbox("echo x > " + quoteProbePath(target))
	if _, err := os.Stat(target); err == nil {
		return errors.New("codex reviewer: the installed Codex does not block writes under the read-only reviewer profile; update Codex")
	}
	return nil
}

func quoteProbePath(path string) string {
	if runtime.GOOS == "windows" {
		return `"` + path + `"`
	}
	return `'` + strings.ReplaceAll(path, `'`, `'"'"'`) + `'`
}

// reviewerArgs adds the read-only profile, the review-guard hook and the
// reviewer's shell environment to a Codex launch.
func (r *Reviewer) reviewerArgs(inv ports.ReviewInvocation) ([]string, error) {
	extra, err := codexReadOnlyArgs(inv)
	if err != nil {
		return nil, err
	}
	guard, err := r.reviewGuardHookArgs()
	if err != nil {
		return nil, err
	}
	return append(extra, guard...), nil
}

// reviewGuardHookArgs registers `ao hooks codex review-guard` as Codex's
// PreToolUse hook on the absolute AO path. Codex runs a command whose hook
// fails, so a launch without a resolvable AO binary is refused rather than
// started without its guard.
func (r *Reviewer) reviewGuardHookArgs() ([]string, error) {
	resolve := r.executable
	if resolve == nil {
		resolve = os.Executable
	}
	executable, err := resolve()
	if err != nil {
		return nil, fmt.Errorf("codex reviewer: resolve AO executable for the review guard: %w", err)
	}
	if !filepath.IsAbs(executable) {
		if executable, err = filepath.Abs(executable); err != nil {
			return nil, fmt.Errorf("codex reviewer: make AO executable absolute: %w", err)
		}
	}
	command, err := json.Marshal(quoteHookExecutable(executable) + " hooks codex review-guard")
	if err != nil {
		return nil, fmt.Errorf("codex reviewer: encode review guard hook: %w", err)
	}
	return []string{"-c", `hooks.PreToolUse=[{hooks=[{type="command",command=` + string(command) + `,timeout=30}]}]`}, nil
}

// quoteHookExecutable quotes the AO path for the shell Codex runs hook
// commands through; PowerShell on Windows needs the call operator.
func quoteHookExecutable(path string) string {
	if runtime.GOOS == "windows" {
		return `& "` + path + `"`
	}
	return `'` + strings.ReplaceAll(path, `'`, `'"'"'`) + `'`
}

func codexReadOnlyArgs(inv ports.ReviewInvocation) ([]string, error) {
	extra := reviewerProfileArgs()
	// Shell commands inherit only Codex's core environment by default. Preserve
	// the AO location overrides the reviewer needs to submit to this daemon.
	// The reviewer identity lets the daemon's reviewer gate refuse session
	// management from this shell (#6842).
	values := map[string]string{
		"AO_PORT":                     os.Getenv("AO_PORT"),
		"AO_DATA_DIR":                 firstNonEmpty(inv.DataDir, os.Getenv("AO_DATA_DIR")),
		"AO_RUN_FILE":                 firstNonEmpty(inv.RunFilePath, os.Getenv("AO_RUN_FILE")),
		"AO_REVIEW_SESSION_ID":        inv.ReviewSessionID,
		"AO_REVIEW_WORKER_SESSION_ID": string(inv.WorkerSessionID),
	}
	for _, name := range []string{"AO_PORT", "AO_DATA_DIR", "AO_RUN_FILE", "AO_REVIEW_SESSION_ID", "AO_REVIEW_WORKER_SESSION_ID"} {
		value := values[name]
		if value == "" {
			continue
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("encode %s: %w", name, err)
		}
		extra = append(extra, "-c", "shell_environment_policy.set."+name+"="+string(encoded))
	}
	return extra, nil
}

// neverApprove rewrites the worker adapter's approval flags to
// `--ask-for-approval never` and drops the auto-reviewer, so nothing can
// approve an escalation out of the read-only sandbox. A launch that bypasses
// approvals and the sandbox entirely is refused rather than repaired.
func neverApprove(argv []string) ([]string, error) {
	out := make([]string, 0, len(argv)+2)
	found := false
	for i := 0; i < len(argv); i++ {
		arg := argv[i]
		switch {
		case arg == "--":
			out = append(out, argv[i:]...)
			i = len(argv)
		case arg == "--dangerously-bypass-approvals-and-sandbox" || arg == "--yolo":
			return nil, fmt.Errorf("codex reviewer: refusing to launch with %s", arg)
		case (arg == "--ask-for-approval" || arg == "-a") && i+1 < len(argv):
			out = append(out, arg, "never")
			found = true
			i++
		case arg == "-c" && i+1 < len(argv) && strings.HasPrefix(argv[i+1], "approvals_reviewer="):
			i++
		default:
			out = append(out, arg)
		}
	}
	if found {
		return out, nil
	}
	// The worker adapter always emits approval flags for the auto policy; if it
	// ever stops, place ours right after the binary (and its resume
	// subcommand), ahead of any positional session id or prompt.
	at := min(1, len(out))
	if at < len(out) && out[at] == "resume" {
		at++
	}
	return slices.Insert(out, at, "--ask-for-approval", "never"), nil
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if value != "" {
			return value
		}
	}
	return ""
}
