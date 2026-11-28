package pi

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/reviewgateway"
)

func testReviewer(help string) *Reviewer {
	return &Reviewer{
		resolveBinary: func(context.Context) (string, error) { return "pi", nil },
		runHelp:       func(context.Context, string) ([]byte, error) { return []byte(help), nil },
	}
}

func testInvocation(t *testing.T, runID, prURL, targetSHA string) ports.ReviewInvocation {
	t.Helper()
	root := t.TempDir()
	promptRoot := filepath.Join(root, "prompts")
	if err := os.MkdirAll(promptRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(promptRoot, runID+"-task.md")
	if err := os.WriteFile(taskPath, []byte("review task"), 0o600); err != nil {
		t.Fatal(err)
	}
	return ports.ReviewInvocation{
		ReviewerID: "review-worker-1", RunID: runID, WorkerSessionID: "worker-1",
		PRURL: prURL, TargetSHA: targetSHA,
		WorkspacePath: filepath.Join(root, "worktree"), DataDir: filepath.Join(root, "ao-data"),
		Prompt: "Read and follow the AO review task.", TaskPromptFile: taskPath, TaskPromptRoot: promptRoot,
	}
}

func readActiveManifest(t *testing.T, pointerPath string) reviewgateway.Manifest {
	t.Helper()
	manifestPath, err := os.ReadFile(pointerPath)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(strings.TrimSpace(string(manifestPath)))
	if err != nil {
		t.Fatal(err)
	}
	var manifest reviewgateway.Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	return manifest
}

func TestExtensionRejectsCommandInjectionSurfaces(t *testing.T) {
	text := string(extensionSource)
	// The model never supplies a command line: the sole process helper receives
	// only fixed executable names, and every subprocess receives an argv array.
	runCall := regexp.MustCompile(`run\(pi,\s*([^,]+),`)
	for _, match := range runCall.FindAllStringSubmatch(text, -1) {
		if match[1] != `"git"` && match[1] != `"gh"` && match[1] != `"ao"` {
			t.Fatalf("extension has non-constant executable in %q", match[0])
		}
	}
	for _, want := range []string{
		`ref.startsWith("-")`,
		`!/^[A-Za-z0-9._/@{}~^+-]+$/.test(ref)`,
		`args.push("--", params.path)`,
		`normalized.split("/").includes("..")`,
		`authorizedTask(params.runId, params.prUrl, params.targetSha)`,
		`task.prUrl !== prUrl || task.targetSha !== targetSha`,
		`commit_id: task.targetSha`,
		`manifest.tasks.some((task) => task.runId === review.runId)`,
		`writeFile(input, JSON.stringify`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("extension missing injection guard %q", want)
		}
	}
	for _, unsafe := range []string{"shell: true", "sh -c", "bash -c", "execSync", "spawn("} {
		if strings.Contains(text, unsafe) {
			t.Fatalf("extension contains unsafe command surface %q", unsafe)
		}
	}
	for _, staleAuthority := range []string{"async function taskText", "tasks.includes(params.prUrl)", "tasks.includes(`run"} {
		if strings.Contains(text, staleAuthority) {
			t.Fatalf("extension retains historical prompt authorization %q", staleAuthority)
		}
	}
}

func TestReviewCommandIsInteractiveAndIsolated(t *testing.T) {
	inv := testInvocation(t, "run-1", "https://github.com/acme/widgets/pull/42", "0123456789abcdef")
	r := testReviewer("")
	inv.Prompt = "Read and follow the AO review task in `/ao/task.md`."
	inv.SystemPromptFile = filepath.Join(inv.TaskPromptRoot, "system.md")
	spec, err := r.ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	joined := strings.Join(spec.Argv, "\n")
	for _, forbidden := range []string{"--print", "-p", "--mode", "json", "rpc"} {
		if slices.Contains(spec.Argv, forbidden) {
			t.Fatalf("interactive reviewer argv contains forbidden %q: %#v", forbidden, spec.Argv)
		}
	}
	for _, required := range append(requiredFlags, "--append-system-prompt") {
		if !strings.Contains(joined, required) {
			t.Fatalf("argv missing %q: %#v", required, spec.Argv)
		}
	}
	if !strings.Contains(joined, "ao_read,ao_search,git_inspect,github_post_review,ao_review_submit") {
		t.Fatalf("argv missing exact tool allowlist: %#v", spec.Argv)
	}
	if got := spec.Argv[len(spec.Argv)-1]; got != "Read and follow the AO review task in `/ao/task.md`." {
		t.Fatalf("terminal-visible prompt = %q", got)
	}
	if spec.Env["AO_PI_REVIEW_SESSION"] != "worker-1" || spec.Env["AO_PI_REVIEW_PROMPT_ROOT"] != inv.TaskPromptRoot || spec.Env["AO_PI_REVIEW_MANIFEST_POINTER"] == "" {
		t.Fatalf("env = %#v", spec.Env)
	}
	wantProfileRoot := filepath.Join(inv.DataDir, "reviewer-runtime", inv.ReviewerID)
	if spec.Env["HOME"] != filepath.Join(wantProfileRoot, "config") ||
		spec.Env["XDG_CONFIG_HOME"] != filepath.Join(wantProfileRoot, "config") ||
		spec.Env["XDG_STATE_HOME"] != filepath.Join(wantProfileRoot, "state") ||
		spec.Env["XDG_CACHE_HOME"] != filepath.Join(wantProfileRoot, "cache") ||
		spec.Env["TMPDIR"] != filepath.Join(wantProfileRoot, "tmp") {
		t.Fatalf("Pi reviewer must use AO-owned profile roots, env = %#v", spec.Env)
	}
	manifest := readActiveManifest(t, spec.Env["AO_PI_REVIEW_MANIFEST_POINTER"])
	if len(manifest.Tasks) != 1 || manifest.Tasks[0].RunID != inv.RunID || manifest.Tasks[0].PRURL != inv.PRURL || manifest.Tasks[0].TargetSHA != inv.TargetSHA {
		t.Fatalf("active manifest = %+v", manifest)
	}
	extensionPath := filepath.Join(inv.TaskPromptRoot, extensionFilename)
	data, err := os.ReadFile(extensionPath)
	if err != nil {
		t.Fatalf("read materialized extension: %v", err)
	}
	text := string(data)
	for _, want := range []string{"--no-pager", "--no-ext-diff", "github_post_review", "ao_review_submit"} {
		if !strings.Contains(text, want) {
			t.Fatalf("extension missing %q", want)
		}
	}
	for _, forbidden := range []string{"createBashTool", `name: "bash"`, "git commit", "git push"} {
		if strings.Contains(text, forbidden) {
			t.Fatalf("extension contains forbidden unrestricted surface %q", forbidden)
		}
	}
}

func TestSequentialReviewMessagesReplaceRatherThanAccumulateAuthority(t *testing.T) {
	r := testReviewer("")
	first := testInvocation(t, "run-1", "https://github.com/acme/widgets/pull/41", "1111111111111111")
	spec, err := r.ReviewCommand(context.Background(), first)
	if err != nil {
		t.Fatal(err)
	}
	pointer := spec.Env["AO_PI_REVIEW_MANIFEST_POINTER"]
	if got := readActiveManifest(t, pointer).Tasks; len(got) != 1 || got[0].RunID != "run-1" {
		t.Fatalf("first active tasks = %+v", got)
	}

	second := first
	second.RunID = "run-2"
	second.PRURL = "https://github.com/acme/widgets/pull/42"
	second.TargetSHA = "2222222222222222"
	second.TaskPromptFile = filepath.Join(second.TaskPromptRoot, "run-2-task.md")
	if err := os.WriteFile(second.TaskPromptFile, []byte("second review task"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := r.ReviewMessage(context.Background(), second); err != nil {
		t.Fatal(err)
	}
	manifest := readActiveManifest(t, pointer)
	if len(manifest.Tasks) != 1 || manifest.Tasks[0].RunID != "run-2" || manifest.Tasks[0].PRURL != second.PRURL || manifest.Tasks[0].TargetSHA != second.TargetSHA {
		t.Fatalf("second active manifest retained stale authority: %+v", manifest)
	}
}

func TestReviewCommandPreflightShapeNeedsNoPromptRoot(t *testing.T) {
	spec, err := testReviewer("").ReviewCommand(context.Background(), ports.ReviewInvocation{WorkspacePath: "/ws"})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if !slices.Equal(spec.Argv, []string{"pi"}) {
		t.Fatalf("argv = %#v", spec.Argv)
	}
}

func TestReviewPreflightRequiresIsolationFlags(t *testing.T) {
	help := strings.Join(requiredFlags, "\n")
	if err := testReviewer(help).ReviewPreflight(context.Background(), "/ws"); err != nil {
		t.Fatalf("ReviewPreflight: %v", err)
	}
	missing := testReviewer(strings.Replace(help, "--no-builtin-tools", "", 1))
	if err := missing.ReviewPreflight(context.Background(), "/ws"); err == nil || !strings.Contains(err.Error(), "--no-builtin-tools") {
		t.Fatalf("missing flag error = %v", err)
	}
}

func TestReviewMessageAndEscapeCancel(t *testing.T) {
	r := testReviewer("")
	inv := testInvocation(t, "run-next", "https://github.com/acme/widgets/pull/43", "3333333333333333")
	inv.Prompt = "next task"
	message, err := r.ReviewMessage(context.Background(), inv)
	if err != nil || message != "next task" {
		t.Fatalf("ReviewMessage = %q, %v", message, err)
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInput || cancel.Input != "\x1b" {
		t.Fatalf("ReviewCancel = %+v, %v", cancel, err)
	}
}
