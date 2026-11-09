package agy

import (
	"context"
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
	binary := filepath.Join(t.TempDir(), "agy")
	reviewer := testReviewer(strings.Join(requiredFlags, "\n"), "1.1.9")
	reviewer.resolveBinary = func(context.Context) (string, error) { return binary, nil }
	spec, err := reviewer.ReviewCommand(context.Background(), ports.ReviewInvocation{
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
	want := []string{binary, "--sandbox", "--add-dir", "/worker", "--add-dir", "/ao/prompts/worker/reviewer", "--prompt-interactive", wantPrompt}
	if !slices.Equal(spec.Argv, want) || spec.WorkingDirectory != "/worker" {
		t.Fatalf("spec = %+v, want argv %#v", spec, want)
	}
	if spec.Env["HOME"] != filepath.Join(dataDir, "reviewer-runtime", "review-worker-1", "config") || spec.Env["AO_DATA_DIR"] != dataDir {
		t.Fatalf("AO-owned environment = %#v", spec.Env)
	}
}

func TestInteractiveArgvIsPermanentTUIOnly(t *testing.T) {
	argv := interactiveArgv("agy", "/worker", "/ao/prompts", "Read and follow `/ao/task.md`.")
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
