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
		hostHome: func() (string, error) { return "", nil },
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
	if err == nil || !strings.Contains(err.Error(), "not signed in") {
		t.Fatalf("signed-out err = %v, want sign-in guidance", err)
	}
	var authErr *ports.ReviewerAuthRequiredError
	if !errors.Is(err, ports.ErrChatAuthRequired) || !errors.As(err, &authErr) {
		t.Fatalf("signed-out err = %#v, want ReviewerAuthRequiredError matching ErrChatAuthRequired", err)
	}
	if !strings.Contains(authErr.Guidance, "`'/opt/agy/bin/agy'`") {
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

func TestReviewCommandSignInProbeReportsLocalCredentialOverride(t *testing.T) {
	dataDir := t.TempDir()
	configRoot := filepath.Join(dataDir, "reviewer-runtime", "review-worker-1", "config")
	localToken := filepath.Join(configRoot, oauthTokenRelPath)
	if err := os.MkdirAll(filepath.Dir(localToken), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localToken, []byte("stale-local-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	inv := ports.ReviewInvocation{
		DataDir:        dataDir,
		ReviewerID:     "review-worker-1",
		TaskPromptRoot: "/ao/prompts",
		WorkspacePath:  "/worker",
		Prompt:         "p",
	}
	_, err := signInReviewer("Error: Please sign in to view available models.", nil).ReviewCommand(context.Background(), inv)
	var authErr *ports.ReviewerAuthRequiredError
	if !errors.As(err, &authErr) {
		t.Fatalf("err = %#v, want ReviewerAuthRequiredError", err)
	}
	if !strings.Contains(authErr.Guidance, "rm "+shellQuote(localToken)) {
		t.Fatalf("guidance = %q, want rm command naming %s", authErr.Guidance, localToken)
	}
	if !strings.Contains(authErr.Guidance, "HOME="+shellQuote(configRoot)+" XDG_CONFIG_HOME="+shellQuote(configRoot)+" '/opt/agy/bin/agy'") {
		t.Fatalf("guidance = %q, want reviewer-home sign-in fallback", authErr.Guidance)
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

func TestSeedHostAuthLinksOAuthTokenAndPreservesExisting(t *testing.T) {
	hostHome := t.TempDir()
	configRoot := t.TempDir()

	// No host token yet: no-op.
	seedHostAuthFrom(configRoot, hostHome)
	dst := filepath.Join(configRoot, ".gemini", "antigravity-cli", oauthTokenFile)
	if _, err := os.Lstat(dst); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("dst should not exist when host has no token, got err=%v", err)
	}

	// Host has token: symlink created.
	src := filepath.Join(hostHome, ".gemini", "antigravity-cli", oauthTokenFile)
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("host-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedHostAuthFrom(configRoot, hostHome)
	target, err := os.Readlink(dst)
	if err != nil || target != src {
		t.Fatalf("dst symlink = %q (err=%v), want %q", target, err, src)
	}

	// Idempotent on second call.
	seedHostAuthFrom(configRoot, hostHome)

	// Existing reviewer-local token is preserved.
	configRoot2 := t.TempDir()
	dst2 := filepath.Join(configRoot2, ".gemini", "antigravity-cli", oauthTokenFile)
	if err := os.MkdirAll(filepath.Dir(dst2), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst2, []byte("local-token"), 0o600); err != nil {
		t.Fatal(err)
	}
	seedHostAuthFrom(configRoot2, hostHome)
	got, err := os.ReadFile(dst2)
	if err != nil || string(got) != "local-token" {
		t.Fatalf("existing local token = %q (err=%v), want local-token", string(got), err)
	}

	// Dangling symlink is replaced with the live host token.
	configRoot3 := t.TempDir()
	dst3 := filepath.Join(configRoot3, ".gemini", "antigravity-cli", oauthTokenFile)
	if err := os.MkdirAll(filepath.Dir(dst3), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(hostHome, "nonexistent-old-token"), dst3); err != nil {
		t.Fatal(err)
	}
	seedHostAuthFrom(configRoot3, hostHome)
	if target, err := os.Readlink(dst3); err != nil || target != src {
		t.Fatalf("replaced symlink = %q (err=%v), want %q", target, err, src)
	}
}

func TestSeedHostAuthLinksLoginKeychainOnly(t *testing.T) {
	hostHome := t.TempDir()
	kcDir := filepath.Join(hostHome, "Library", "Keychains")
	if err := os.MkdirAll(filepath.Join(kcDir, "local-items-uuid"), 0o700); err != nil {
		t.Fatal(err)
	}
	kcSrc := filepath.Join(kcDir, "login.keychain-db")
	if err := os.WriteFile(kcSrc, []byte("keychain"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Links only login.keychain-db, not the whole Keychains directory.
	configRoot := t.TempDir()
	seedHostAuthFrom(configRoot, hostHome)
	target, err := os.Readlink(filepath.Join(configRoot, "Library", "Keychains", "login.keychain-db"))
	if err != nil || target != kcSrc {
		t.Fatalf("login.keychain-db symlink = %q (err=%v), want %q", target, err, kcSrc)
	}
	if _, err := os.Lstat(filepath.Join(configRoot, "Library", "Keychains", "local-items-uuid")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("should not expose sibling Keychains entries, got err=%v", err)
	}
}

func TestReviewCommandBestEffortLinkFailureDoesNotBlockLaunch(t *testing.T) {
	hostHome := t.TempDir()
	kcSrc := filepath.Join(hostHome, "Library", "Keychains", "login.keychain-db")
	if err := os.MkdirAll(filepath.Dir(kcSrc), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(kcSrc, []byte("keychain"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Place a regular file at configRoot/Library so MkdirAll(Library/Keychains)
	// fails during seedHostAuth. Launch must still fall through to verifySignedIn.
	dataDir := t.TempDir()
	configRoot := filepath.Join(dataDir, "reviewer-runtime", "review-worker-1", "config")
	if err := os.MkdirAll(configRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(configRoot, "Library"), []byte("blocker"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := signInReviewer("gateway-model\tGateway Model\n", nil)
	r.hostHome = func() (string, error) { return hostHome, nil }
	inv := ports.ReviewInvocation{
		DataDir:        dataDir,
		ReviewerID:     "review-worker-1",
		TaskPromptRoot: "/ao/prompts",
		WorkspacePath:  "/worker",
		Prompt:         "p",
	}
	if _, err := r.ReviewCommand(context.Background(), inv); err != nil {
		t.Fatalf("ReviewCommand should not fail when best-effort host auth link fails: %v", err)
	}
}

func TestReviewCommandReusesHostAuthBeforeSignInProbe(t *testing.T) {
	hostHome := t.TempDir()
	src := filepath.Join(hostHome, ".gemini", "antigravity-cli", oauthTokenFile)
	if err := os.MkdirAll(filepath.Dir(src), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(src, []byte("host-token"), 0o600); err != nil {
		t.Fatal(err)
	}

	r := testReviewer(strings.Join(requiredFlags, "\n"), "1.1.9")
	r.hostHome = func() (string, error) { return hostHome, nil }
	base := r.run
	r.run = func(ctx context.Context, env map[string]string, binary string, args ...string) ([]byte, error) {
		if slices.Equal(args, []string{"models"}) {
			tokenPath := filepath.Join(env["HOME"], ".gemini", "antigravity-cli", oauthTokenFile)
			_, statErr := os.Stat(tokenPath)
			signedIn := statErr == nil
			if !signedIn {
				// `agy models` exits 0 even when signed out.
				return []byte("Error: Please sign in to view available models."), nil
			}
			return []byte("gemini-4-neon-high\tGemini 4 Neon (High)\n"), nil
		}
		return base(ctx, env, binary, args...)
	}

	inv := ports.ReviewInvocation{
		DataDir:        t.TempDir(),
		ReviewerID:     "review-worker-1",
		TaskPromptRoot: "/ao/prompts",
		WorkspacePath:  "/worker",
		Prompt:         "p",
	}
	if _, err := r.ReviewCommand(context.Background(), inv); err != nil {
		t.Fatalf("ReviewCommand should succeed using linked host token: %v", err)
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
	if got := readTrusted(t, filepath.Join(spec.Env["HOME"], ".gemini", "antigravity-cli", "settings.json")); !slices.Equal(got, []string{"/worker"}) {
		t.Fatalf("trusted workspaces = %#v, want only the reviewer workspace", got)
	}
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

func TestSeedTrustedWorkspaceFreshSettingsTrustsOnlyWorkspace(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(t.TempDir(), "worktrees", "worker-1")
	if err := os.MkdirAll(workspace, 0o700); err != nil {
		t.Fatal(err)
	}
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedTrustedWorkspace(root, workspace); err != nil {
		t.Fatalf("seedTrustedWorkspace: %v", err)
	}
	path := filepath.Join(root, ".gemini", "antigravity-cli", "settings.json")
	want := []string{workspace}
	if resolved != workspace {
		want = append(want, resolved)
	}
	got := readTrusted(t, path)
	if !slices.Equal(got, want) {
		t.Fatalf("trusted = %#v, want %#v", got, want)
	}
	home, _ := os.UserHomeDir()
	for _, entry := range got {
		if entry == filepath.Dir(workspace) || entry == home || entry == "/" || entry == "~" {
			t.Fatalf("trust broadened to %q", entry)
		}
	}
	info, err := os.Stat(path)
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("settings mode = %v, %v; want 0600", info, err)
	}
}

func TestSeedTrustedWorkspacePreservesExistingSettings(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".gemini", "antigravity-cli", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"permissions":{"allow":["Read"]},"trustedWorkspaces":["/existing/project",7]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	for range 2 { // idempotent: a retry adds nothing
		if err := seedTrustedWorkspace(root, "/worker"); err != nil {
			t.Fatalf("seedTrustedWorkspace: %v", err)
		}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	settings := map[string]json.RawMessage{}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatal(err)
	}
	if string(settings["permissions"]) != `{"allow":["Read"]}` {
		t.Fatalf("unrelated settings not preserved: %s", raw)
	}
	if string(settings["trustedWorkspaces"]) != `["/existing/project",7,"/worker"]` {
		t.Fatalf("trustedWorkspaces = %s, want existing entries kept and /worker added once", settings["trustedWorkspaces"])
	}
}

func TestSeedTrustedWorkspaceScopeAndInvalidInput(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, ".gemini", "antigravity-cli", "settings.json")
	for _, workspace := range []string{"", "   ", "relative/worker"} {
		if err := seedTrustedWorkspace(root, workspace); err != nil {
			t.Fatalf("seedTrustedWorkspace(%q): %v", workspace, err)
		}
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty or relative workspace wrote settings: %v", err)
	}

	// A symlinked workspace trusts the link and its target, nothing above them.
	base := t.TempDir()
	target := filepath.Join(base, "real", "worker")
	if err := os.MkdirAll(target, 0o700); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "link")
	if err := os.Symlink(target, link); err != nil {
		t.Fatal(err)
	}
	resolvedTarget, err := filepath.EvalSymlinks(target)
	if err != nil {
		t.Fatal(err)
	}
	if err := seedTrustedWorkspace(root, link+"/"); err != nil {
		t.Fatal(err)
	}
	if got := readTrusted(t, path); !slices.Equal(got, []string{link, resolvedTarget}) {
		t.Fatalf("symlink trusted = %#v, want [%s %s]", got, link, resolvedTarget)
	}

	// Corrupt settings and a non-list trust value are replaced.
	for _, content := range []string{"not json", `{"trustedWorkspaces":"oops"}`} {
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := seedTrustedWorkspace(root, "/worker"); err != nil {
			t.Fatalf("seedTrustedWorkspace(%q): %v", content, err)
		}
		if got := readTrusted(t, path); !slices.Equal(got, []string{"/worker"}) {
			t.Fatalf("after %q trusted = %#v", content, got)
		}
	}
}

func readTrusted(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read settings: %v", err)
	}
	var settings struct {
		TrustedWorkspaces []string `json:"trustedWorkspaces"`
	}
	if err := json.Unmarshal(raw, &settings); err != nil {
		t.Fatalf("decode settings %q: %v", raw, err)
	}
	return settings.TrustedWorkspaces
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
