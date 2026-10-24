package systeminstall

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type verifierAgent struct {
	ports.Agent
	path       string
	resolveErr error
	resolve    func(context.Context) (string, error)
	authCalls  *atomic.Int32
}

func (a verifierAgent) ResolveBinary(ctx context.Context) (string, error) {
	if a.resolve != nil {
		return a.resolve(ctx)
	}
	return a.path, a.resolveErr
}
func (a verifierAgent) AuthStatus(context.Context) (ports.AgentAuthStatus, error) {
	a.authCalls.Add(1)
	return ports.AgentAuthStatusAuthorized, nil
}

type verifierResolver map[domain.AgentHarness]ports.Agent

func (r verifierResolver) Agent(harness domain.AgentHarness) (ports.Agent, bool) {
	agent, ok := r[harness]
	return agent, ok
}

type recordingCommandRunner struct {
	argv []string
	run  func(context.Context, []string, io.Writer, io.Writer) error
}

func (r *recordingCommandRunner) Run(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	r.argv = append([]string(nil), argv...)
	if r.run != nil {
		return r.run(ctx, argv, stdout, stderr)
	}
	_, _ = io.WriteString(stdout, "codex-cli 1.2.3\n")
	return nil
}

func TestVerifierUsesAdapterResolvedBinaryWithoutAuthProbe(t *testing.T) {
	t.Parallel()
	binary := verifierBinaryFixture(t, "codex")
	var authCalls atomic.Int32
	runner := &recordingCommandRunner{}
	verifier := NewVerifier(verifierResolver{
		domain.HarnessCodex: verifierAgent{path: binary, authCalls: &authCalls},
	}, runner)

	result, err := verifier.Verify(context.Background(), TargetCodex)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if result.ResolvedPath != binary || result.Output != "codex-cli 1.2.3\n" {
		t.Fatalf("result = %+v", result)
	}
	if !reflect.DeepEqual(runner.argv, []string{binary, "--version"}) {
		t.Fatalf("argv = %v, want exact resolved binary version probe", runner.argv)
	}
	if authCalls.Load() != 0 {
		t.Fatalf("auth probe calls = %d, want 0", authCalls.Load())
	}
}

func TestVerifierRejectsMissingAdapterOrBinaryResolver(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name     string
		resolver verifierResolver
	}{
		{name: "adapter missing", resolver: verifierResolver{}},
		{name: "binary capability missing", resolver: verifierResolver{domain.HarnessCodex: struct{ ports.Agent }{}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewVerifier(tt.resolver, &recordingCommandRunner{}).Verify(context.Background(), TargetCodex)
			if err == nil {
				t.Fatal("Verify error = nil")
			}
		})
	}
}

func TestVerifierBoundsVersionProbe(t *testing.T) {
	t.Parallel()
	binary := verifierBinaryFixture(t, "codex")
	runner := &recordingCommandRunner{run: func(ctx context.Context, _ []string, _, _ io.Writer) error {
		<-ctx.Done()
		// The host runner kills the probe's process group at the deadline, so
		// its error is the child's exit status rather than ctx.Err().
		return errors.New("signal: killed")
	}}
	verifier := NewVerifier(verifierResolver{
		domain.HarnessCodex: verifierAgent{path: binary, authCalls: &atomic.Int32{}},
	}, runner)
	verifier.timeout = 20 * time.Millisecond

	_, err := verifier.Verify(context.Background(), TargetCodex)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Verify error = %v, want deadline exceeded", err)
	}
	for _, want := range []string{"codex version probe", "timed out after 20ms", "Verify again"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("Verify error = %q, want it to contain %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "signal: killed") {
		t.Fatalf("Verify error = %q, want the timeout named instead of the kill signal", err)
	}
}

func TestVerifierReportsIdentityResolutionTimeout(t *testing.T) {
	t.Parallel()
	synctest.Test(t, func(t *testing.T) {
		runner := &recordingCommandRunner{}
		verifier := NewVerifier(verifierResolver{
			domain.HarnessGoose: verifierAgent{resolve: func(ctx context.Context) (string, error) {
				<-ctx.Done()
				return "", ctx.Err()
			}},
		}, runner)
		verifier.timeout = 20 * time.Millisecond
		_, err := verifier.Verify(t.Context(), TargetGoose)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Verify error = %v, want deadline exceeded", err)
		}
		for _, want := range []string{"goose binary identity check", "timed out after 20ms", "Verify again"} {
			if !strings.Contains(err.Error(), want) {
				t.Fatalf("Verify error = %q, want %q", err, want)
			}
		}
		if runner.argv != nil {
			t.Fatal("version probe ran before identity was confirmed")
		}
	})
}

func TestVerifierKeepsNonTimeoutProbeFailure(t *testing.T) {
	t.Parallel()
	binary := verifierBinaryFixture(t, "codex")
	runner := &recordingCommandRunner{run: func(context.Context, []string, io.Writer, io.Writer) error {
		return errors.New("exit status 1")
	}}
	verifier := NewVerifier(verifierResolver{
		domain.HarnessCodex: verifierAgent{path: binary, authCalls: &atomic.Int32{}},
	}, runner)

	_, err := verifier.Verify(context.Background(), TargetCodex)
	if err == nil || err.Error() != "run codex version probe: exit status 1" {
		t.Fatalf("Verify error = %v, want the probe's own failure", err)
	}
}

// A freshly installed Cursor CLI took ~12s to answer its first --version on an
// Intel Mac while macOS assessed the new binary (#5887). The default bound must
// let that first post-install verification succeed instead of killing it.
func TestVerifierDefaultTimeoutAllowsSlowFirstLaunch(t *testing.T) {
	t.Parallel()
	binary := verifierBinaryFixture(t, "cursor-agent")
	synctest.Test(t, func(t *testing.T) {
		runner := &recordingCommandRunner{run: func(ctx context.Context, _ []string, stdout, _ io.Writer) error {
			select {
			case <-time.After(12 * time.Second):
				_, _ = io.WriteString(stdout, "2026.10.01-e373342\n")
				return nil
			case <-ctx.Done():
				return errors.New("signal: killed")
			}
		}}
		verifier := NewVerifier(verifierResolver{
			domain.HarnessCursor: verifierAgent{path: binary, authCalls: &atomic.Int32{}},
		}, runner)

		result, err := verifier.Verify(t.Context(), TargetCursor)
		if err != nil {
			t.Fatalf("Verify: %v", err)
		}
		if result.ResolvedPath != binary || result.Output != "2026.10.01-e373342\n" {
			t.Fatalf("result = %+v", result)
		}
	})
}

func verifierBinaryFixture(t *testing.T, name string) string {
	t.Helper()
	binary := filepath.Join(t.TempDir(), name+".exe")
	if err := os.WriteFile(binary, []byte("MZ native fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	return binary
}

func TestVerifierCoversEveryAgentTarget(t *testing.T) {
	// Iterate the production target list so adding an agent without executable
	// ownership coverage fails here before discovery or update checks ship.
	for _, target := range agentTargets {
		t.Run(string(target), func(t *testing.T) {
			name := string(target)
			switch target {
			case TargetCursor:
				name = "cursor-agent"
			case TargetContinue:
				name = "cn"
			case TargetOpencodeV2:
				name = "opencode2"
			}
			binary := verifierBinaryFixture(t, name)
			runner := &recordingCommandRunner{}
			verifier := NewVerifier(verifierResolver{domain.AgentHarness(target): verifierAgent{path: binary}}, runner)
			result, err := verifier.Verify(t.Context(), target)
			if err != nil {
				t.Fatalf("agent target %q lacks executable identity coverage for %q: %v", target, name, err)
			}
			if result.ResolvedPath != binary || !reflect.DeepEqual(runner.argv, []string{binary, "--version"}) {
				t.Fatalf("Verify = %+v, command = %v, want selected executable %q", result, runner.argv, binary)
			}
		})
	}
}

func TestVerifierRejectsRedirectedVersionProbe(t *testing.T) {
	for _, target := range []Target{TargetCursor, TargetCodex, TargetClaudeCode, TargetGrok} {
		t.Run(string(target), func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "agent.cmd")
			if err := os.WriteFile(binary, []byte("@call autohand %*\n"), 0o700); err != nil {
				t.Fatal(err)
			}
			runner := &recordingCommandRunner{}
			verifier := NewVerifier(verifierResolver{domain.AgentHarness(target): verifierAgent{path: binary}}, runner)
			result, err := verifier.Verify(t.Context(), target)
			if err == nil || !strings.Contains(err.Error(), "autohand") {
				t.Fatalf("Verify = %v, want refusal", err)
			}
			if result.ResolvedPath != binary {
				t.Fatalf("diagnostic path = %q, want %q", result.ResolvedPath, binary)
			}
			if runner.argv != nil {
				t.Fatalf("unsafe version command ran: %v", runner.argv)
			}
		})
	}
}

func TestVerifierRechecksCachedPathBeforeEveryVersionProbe(t *testing.T) {
	root := t.TempDir()
	binary := filepath.Join(root, "cursor-agent.cmd")
	native := filepath.Join(root, "cursor-agent.exe")
	if err := os.WriteFile(native, []byte("MZ native fixture"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(binary, []byte("@\"%~dp0cursor-agent.exe\" %*\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	runner := &recordingCommandRunner{}
	verifier := NewVerifier(verifierResolver{domain.HarnessCursor: verifierAgent{path: binary}}, runner)
	if _, err := verifier.Verify(t.Context(), TargetCursor); err != nil {
		t.Fatal(err)
	}
	runner.argv = nil
	if err := os.WriteFile(binary, []byte("@call autohand %*\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := verifier.Verify(t.Context(), TargetCursor); err == nil || !strings.Contains(err.Error(), "autohand") {
		t.Fatalf("replaced cached launcher = %v", err)
	}
	if runner.argv != nil {
		t.Fatal("version probe ran after launcher replacement")
	}
}
