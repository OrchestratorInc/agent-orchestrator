package agy

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func testReviewer(help, version string) *Reviewer {
	return &Reviewer{
		resolveBinary: func(context.Context) (string, error) { return "/opt/agy/bin/agy", nil },
		run: func(_ context.Context, _ map[string]string, _ string, args ...string) ([]byte, error) {
			if slices.Equal(args, []string{"--help"}) {
				return []byte(help), nil
			}
			return []byte(version), nil
		},
	}
}

// signInReviewer answers `agy models` with the given output and error.
func signInReviewer(models string, modelsErr error) *Reviewer {
	r := testReviewer(strings.Join(requiredFlags, "\n"), "1.1.9")
	base := r.run
	r.run = func(ctx context.Context, env map[string]string, binary string, args ...string) ([]byte, error) {
		if slices.Equal(args, []string{"models"}) {
			return []byte(models), modelsErr
		}
		return base(ctx, env, binary, args...)
	}
	return r
}

func TestReviewCommandSignInProbe(t *testing.T) {
	inv := func(dataDir string) ports.ReviewInvocation {
		return ports.ReviewInvocation{DataDir: dataDir, ReviewerID: "review-worker-1", TaskPromptRoot: "/ao/prompts", WorkspacePath: "/worker", Prompt: "p"}
	}

	dataDir := t.TempDir()
	_, err := signInReviewer("Fetching available models... Error: Please sign in to view available models.", nil).ReviewCommand(context.Background(), inv(dataDir))
	home := filepath.Join(dataDir, "reviewer-runtime", "review-worker-1", "config")
	if err == nil || !strings.Contains(err.Error(), "not signed in") || !strings.Contains(err.Error(), home) {
		t.Fatalf("signed-out err = %v, want sign-in guidance naming %s", err, home)
	}
	var authErr *ports.ReviewerAuthRequiredError
	if !errors.Is(err, ports.ErrChatAuthRequired) || !errors.As(err, &authErr) || !strings.Contains(authErr.Guidance, home) {
		t.Fatalf("signed-out err = %#v, want ReviewerAuthRequiredError matching ErrChatAuthRequired", err)
	}
	if !strings.Contains(authErr.Guidance, "HOME="+shellQuote(home)+" XDG_CONFIG_HOME="+shellQuote(home)+" '/opt/agy/bin/agy'") {
		t.Fatalf("guidance = %q, want single-quoted sign-in command", authErr.Guidance)
	}

	for name, r := range map[string]*Reviewer{
		"signed in":   signInReviewer("gemini-3-flash\tGemini 3 Flash\n", nil),
		"probe error": signInReviewer("", errors.New("deadline exceeded")),
	} {
		spec, err := r.ReviewCommand(context.Background(), inv(t.TempDir()))
		if err != nil || !slices.Contains(spec.Argv, "--prompt-interactive") {
			t.Fatalf("%s: spec = %+v, err = %v; want launch", name, spec, err)
		}
	}
}

func TestReviewCommandSignInProbeUsesProjectEnv(t *testing.T) {
	const gateway = "AGY_LLM_GATEWAY_URL"
	var probeEnv map[string]string
	r := testReviewer(strings.Join(requiredFlags, "\n"), "1.1.9")
	base := r.run
	r.run = func(ctx context.Context, env map[string]string, binary string, args ...string) ([]byte, error) {
		if slices.Equal(args, []string{"models"}) {
			probeEnv = env
			if env[gateway] == "" {
				return []byte("Error: Please sign in to view available models."), nil
			}
			return []byte("gateway-model\tGateway Model\n"), nil
		}
		return base(ctx, env, binary, args...)
	}
	dataDir := t.TempDir()
	inv := ports.ReviewInvocation{
		DataDir: dataDir, ReviewerID: "review-worker-1", TaskPromptRoot: "/ao/prompts", WorkspacePath: "/worker", Prompt: "p",
		ProjectEnv: map[string]string{gateway: "https://gateway.example", "HOME": "/project/home"},
	}
	if _, err := r.ReviewCommand(context.Background(), inv); err != nil {
		t.Fatalf("ReviewCommand with project gateway env: %v", err)
	}
	if probeEnv[gateway] != "https://gateway.example" {
		t.Fatalf("probe env missing project %s: %#v", gateway, probeEnv)
	}
	if want := filepath.Join(dataDir, "reviewer-runtime", "review-worker-1", "config"); probeEnv["HOME"] != want {
		t.Fatalf("probe HOME = %q, want reviewer home %q", probeEnv["HOME"], want)
	}
}

func TestShellQuoteKeepsPathsLiteral(t *testing.T) {
	for in, want := range map[string]string{
		"/ao/reviewer home/config": `'/ao/reviewer home/config'`,
		"/data/$USER/config":       `'/data/$USER/config'`,
		"/data/it's/config":        `'/data/it'"'"'s/config'`,
	} {
		if got := shellQuote(in); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", in, got, want)
		}
	}
}

func TestReviewCommandPreflightShapeOnlyResolvesBinary(t *testing.T) {
	spec, err := testReviewer("", "").ReviewCommand(context.Background(), ports.ReviewInvocation{WorkspacePath: "/worker"})
	if err != nil {
		t.Fatalf("ReviewCommand: %v", err)
	}
	if !slices.Equal(spec.Argv, []string{"/opt/agy/bin/agy"}) || spec.WorkingDirectory != "" || len(spec.Env) != 0 {
		t.Fatalf("preflight spec = %+v", spec)
	}
}

func TestReviewCommandLaunchesHostTrustedInteractiveTUI(t *testing.T) {
	dataDir := t.TempDir()
	spec, err := testReviewer(strings.Join(requiredFlags, "\n"), "1.1.9").ReviewCommand(context.Background(), ports.ReviewInvocation{
		DataDir:          dataDir,
		ReviewerID:       "review-worker-1",
		TaskPromptRoot:   "/ao/prompts/worker/reviewer",
		WorkspacePath:    "/worker",
		SystemPromptFile: "/ao/prompts/worker/reviewer/system.md",
		Prompt:           "Read and follow `/ao/task.md`.",
	})
	if err != nil {
		t.Fatal(err)
	}
	wantPrompt := "Read and follow the reviewer system instructions at `/ao/prompts/worker/reviewer/system.md`, then Read and follow `/ao/task.md`."
	want := []string{"/opt/agy/bin/agy", "--sandbox", "--add-dir", "/worker", "--add-dir", "/ao/prompts/worker/reviewer", "--prompt-interactive", wantPrompt}
	if !slices.Equal(spec.Argv, want) || spec.WorkingDirectory != "/worker" {
		t.Fatalf("spec = %+v, want argv %#v", spec, want)
	}
	if spec.Env["HOME"] != filepath.Join(dataDir, "reviewer-runtime", "review-worker-1", "config") || spec.Env["AO_DATA_DIR"] != dataDir {
		t.Fatalf("AO-owned environment = %#v", spec.Env)
	}
	assertOnboardingComplete(t, filepath.Join(spec.Env["HOME"], ".gemini", "antigravity-cli", "cache", "onboarding.json"))
}

func TestReviewCommandPassesConfiguredModel(t *testing.T) {
	inv := ports.ReviewInvocation{
		DataDir:        t.TempDir(),
		ReviewerID:     "review-worker-1",
		TaskPromptRoot: "/ao/prompts/worker/reviewer",
		WorkspacePath:  "/worker",
		Prompt:         "Read and follow `/ao/task.md`.",
	}
	inv.Config.Model = "  gemini-3-pro  "
	spec, err := testReviewer(strings.Join(requiredFlags, "\n"), "1.1.9").ReviewCommand(context.Background(), inv)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"/opt/agy/bin/agy", "--sandbox", "--model", "gemini-3-pro", "--add-dir", "/worker", "--add-dir", "/ao/prompts/worker/reviewer", "--prompt-interactive", "Read and follow `/ao/task.md`."}
	if !slices.Equal(spec.Argv, want) {
		t.Fatalf("argv = %#v, want %#v", spec.Argv, want)
	}
}

func TestInteractiveArgvModelFlag(t *testing.T) {
	for _, tc := range []struct {
		model string
		want  []string
	}{
		{"", []string{"agy", "--sandbox", "--prompt-interactive", "p"}},
		{"   ", []string{"agy", "--sandbox", "--prompt-interactive", "p"}},
		{"gemini-3-flash", []string{"agy", "--sandbox", "--model", "gemini-3-flash", "--prompt-interactive", "p"}},
	} {
		if got := interactiveArgv("agy", "", "", tc.model, "p"); !slices.Equal(got, tc.want) {
			t.Errorf("interactiveArgv(model=%q) = %#v, want %#v", tc.model, got, tc.want)
		}
	}
}

func TestSeedOnboardingCompletePreservesExistingState(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".gemini", "antigravity-cli", "cache", "onboarding.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"consumerOnboardingComplete":false,"enterpriseOnboardingComplete":false,"onboardingComplete":false,"other":"kept"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedOnboardingComplete(root); err != nil {
		t.Fatalf("seedOnboardingComplete: %v", err)
	}
	state := assertOnboardingComplete(t, path)
	if string(state["other"]) != `"kept"` {
		t.Fatalf("existing keys not preserved: %s", state)
	}

	// Corrupt state is replaced rather than blocking the reviewer launch.
	if err := os.WriteFile(path, []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := seedOnboardingComplete(root); err != nil {
		t.Fatalf("seedOnboardingComplete(corrupt): %v", err)
	}
	assertOnboardingComplete(t, path)

	// Unreadable (write-only) state is replaced with a private, readable file.
	if err := os.Chmod(path, 0o200); err != nil {
		t.Fatal(err)
	}
	if err := seedOnboardingComplete(root); err != nil {
		t.Fatalf("seedOnboardingComplete(unreadable): %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Fatalf("onboarding state mode = %o, want 600", mode)
	}
	assertOnboardingComplete(t, path)
}

func assertOnboardingComplete(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read onboarding state: %v", err)
	}
	state := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &state); err != nil {
		t.Fatalf("decode onboarding state %q: %v", raw, err)
	}
	for _, key := range []string{"consumerOnboardingComplete", "enterpriseOnboardingComplete", "onboardingComplete"} {
		if string(state[key]) != "true" {
			t.Fatalf("%s = %s, want true (state %s)", key, state[key], raw)
		}
	}
	return state
}

func TestInteractiveArgvIsPermanentTUIOnly(t *testing.T) {
	argv := interactiveArgv("agy", "/worker", "/ao/prompts", "", "Read and follow `/ao/task.md`.")
	want := []string{"agy", "--sandbox", "--add-dir", "/worker", "--add-dir", "/ao/prompts", "--prompt-interactive", "Read and follow `/ao/task.md`."}
	if !slices.Equal(argv, want) {
		t.Fatalf("argv = %#v, want %#v", argv, want)
	}
	for _, forbidden := range []string{"--print", "-p", "--prompt", "--output-format", "json", "stream-json", "rpc"} {
		if slices.Contains(argv, forbidden) {
			t.Fatalf("interactive argv contains forbidden %q: %#v", forbidden, argv)
		}
	}
}

func TestCompatibilityProbeRequiresCurrentInteractiveSurface(t *testing.T) {
	help := strings.Join(requiredFlags, "\n")
	r := testReviewer(help, "1.1.9")
	if err := r.verifyCompatibility(context.Background(), "/opt/agy/bin/agy", map[string]string{"HOME": "/ao/profile"}); err != nil {
		t.Fatalf("verifyCompatibility: %v", err)
	}

	missing := testReviewer(strings.Replace(help, "--prompt-interactive", "", 1), "1.1.9")
	if err := missing.verifyCompatibility(context.Background(), "/opt/agy/bin/agy", nil); err == nil || !strings.Contains(err.Error(), "--prompt-interactive") {
		t.Fatalf("missing flag err = %v", err)
	}
	old := testReviewer(help, "1.1.5")
	if err := old.verifyCompatibility(context.Background(), "/opt/agy/bin/agy", nil); err == nil || !strings.Contains(err.Error(), minimumVersion) {
		t.Fatalf("old version err = %v", err)
	}
}

func TestProductionPreflightResolvesHostTrustedCLI(t *testing.T) {
	if err := testReviewer("", "").ReviewPreflight(context.Background(), "/worker"); err != nil {
		t.Fatalf("ReviewPreflight err = %v", err)
	}
}

func TestReviewMessageAndSingleInterruptCancel(t *testing.T) {
	r := testReviewer("", "")
	message, err := r.ReviewMessage(context.Background(), ports.ReviewInvocation{Prompt: "next task"})
	if err != nil || message != "next task" {
		t.Fatalf("ReviewMessage = %q, %v", message, err)
	}
	cancel, err := r.ReviewCancel(context.Background())
	if err != nil || cancel.Mode != ports.ReviewCancelInterrupt || cancel.Interrupts != 1 {
		t.Fatalf("ReviewCancel = %+v, %v", cancel, err)
	}
}

func TestHostTrustWarningIsExplicit(t *testing.T) {
	for _, phrase := range []string{"host-trusted", "not OS isolation", "built-in tools"} {
		if !strings.Contains(HostTrustWarning, phrase) {
			t.Fatalf("warning %q missing %q", HostTrustWarning, phrase)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		got  string
		want bool
	}{{"1.1.6", true}, {"v1.1.9", true}, {"1.2.0", true}, {"2.0.0", true}, {"1.1.5", false}, {"invalid", false}} {
		if got := versionAtLeast(tc.got, minimumVersion); got != tc.want {
			t.Errorf("versionAtLeast(%q) = %v, want %v", tc.got, got, tc.want)
		}
	}
}
