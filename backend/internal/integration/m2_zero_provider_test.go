package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Zero-provider proof.
//
// The M2 acceptance requires a multi-agent Fake E2E that proves the real stack works while
// remaining provably offline. This file is the evidence for "offline", and it is deliberately
// narrow: it asserts only things that are checkable without a fake provider detector.
//
// What is NOT claimed here, because it cannot be proven from inside a test: "no child process
// was ever started". The right proof of that is structural, not observational — the scripted
// adapter imports no exec, os/exec, or net package (see its own file list), and the fake
// SessionSpawner writes rows instead of launching anything. A test that scanned for processes
// would be timing-dependent and would prove nothing about the code path.

// TestM2E2EStartsNoProviderProcess is the structural half: the E2E fixture's only driver is the
// scripted adapter, and the only install check is that adapter's Probe.
//
// If a real driver were ever wired into this package, the fixture would fail to build this
// assertion rather than silently spawning a provider.
func TestM2E2EStartsNoProviderProcess(t *testing.T) {
	env := newM2Env(t, openM2Store(t))

	for harness, driver := range env.drivers {
		caps, err := driver.Probe(context.Background())
		if err != nil {
			t.Fatalf("probe %s: %v", harness, err)
		}
		// A scripted driver answers from configuration. A real driver would have had to talk to
		// a binary to produce this, which is exactly what this fixture must never do.
		if len(caps) == 0 {
			t.Fatalf("%s probe returned no capabilities, so this proves nothing", harness)
		}
		if got := driver.SendCalls(); got != 0 {
			t.Fatalf("%s had %d turns before any test sent one", harness, got)
		}
	}

	// The capability probe the resolver uses is the real ChatDriverProbe over those same
	// scripted drivers: one install check, no handshake process.
	snapshot, err := env.resolver.Resolve(context.Background(), ports.CapabilityProbeRequest{
		Harness: m2SteerHarness,
	})
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if snapshot.Source != domain.CapabilitySourceProviderHandshake {
		t.Fatalf("source = %q, want provider_handshake from the scripted handshake",
			snapshot.Source)
	}
	if snapshot.AuthState != domain.AgentAuthenticationAuthorized ||
		snapshot.Readiness != domain.CapabilityReady {
		t.Fatalf("readiness = %q/%q, want authorized/ready", snapshot.AuthState, snapshot.Readiness)
	}
	// The executable is never resolved: a scripted driver has no binary, and a snapshot that
	// named one would mean something launched.
	if snapshot.Executable != "" {
		t.Fatalf("snapshot named executable %q; nothing should have been located", snapshot.Executable)
	}
}

// TestM2E2ENeedsNoProviderEnvironment proves the stack runs with no provider credential in the
// environment at all.
//
// It does not fabricate a "was this read" detector. It asserts the honest, checkable fact: the
// fixture is constructed and driven to a real bind with these variables unset, so nothing in
// the path could have depended on them.
func TestM2E2ENeedsNoProviderEnvironment(t *testing.T) {
	for _, key := range []string{
		"ANTHROPIC_API_KEY", "OPENAI_API_KEY", "CODEX_API_KEY", "DEEPSEEK_API_KEY",
	} {
		if value := os.Getenv(key); value != "" {
			// The machine happens to have a credential. Unset it for this test so the claim is
			// about the code path rather than about the developer's environment.
			t.Setenv(key, "")
		}
	}

	env := newM2Env(t, openM2Store(t))
	workflowID, tasks := env.createWorkflow(t, "zero-provider")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "zero-provider")
	bound := env.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "zero-provider", "work")
	if bound.Status != 200 {
		t.Fatalf("bind with no provider credentials: status=%d body=%s", bound.Status, bound.Raw)
	}
	if got := env.spawner.m2SpawnCount(); got != 1 {
		t.Fatalf("spawns = %d, want 1", got)
	}
	// The only turn counter that moved is the scripted driver's, and only for what the test
	// itself sent.
	if got := env.drivers[m2SteerHarness].SendCalls(); got != 0 {
		t.Fatalf("%s sent %d turn(s) without one being requested", m2SteerHarness, got)
	}
}

// TestM2E2ENeverTouchesAUserDataDirectory proves AO state stays under the test's own temp dir.
//
// A real daemon resolves its data dir under ~/.ao; nothing in this fixture should resolve or
// create anything there. The check is on the environment variable the daemon honours: the
// fixture never sets it, and never needs to.
func TestM2E2ENeverTouchesAUserDataDirectory(t *testing.T) {
	if value := os.Getenv("AO_DATA_DIR"); value != "" {
		t.Skipf("AO_DATA_DIR is set in this environment (%q); the assertion would be about "+
			"the shell, not the fixture", value)
	}

	env := newM2Env(t, openM2Store(t))
	workflowID, tasks := env.createWorkflow(t, "no-user-data")
	attemptID, _ := env.claimAndStart(t, workflowID, tasks[m2SteerHarness], "no-user-data")
	bound := env.bindAttempt(t, workflowID, attemptID, m2SteerHarness, "no-user-data", "work")
	if bound.Status != 200 {
		t.Fatalf("bind: status=%d body=%s", bound.Status, bound.Raw)
	}

	// Every path this fixture produced lives under the test's temp dir.
	sessionID := domain.SessionID(str(dig(bound.Body, "binding", "sessionId")))
	record, found, err := env.store.GetSession(context.Background(), sessionID)
	if err != nil || !found {
		t.Fatalf("read session: found=%v err=%v", found, err)
	}
	if record.Metadata.WorkspacePath == "" {
		t.Fatal("the session names no workspace, so the path assertion would prove nothing")
	}
	if !isUnderTempDir(t, record.Metadata.WorkspacePath) {
		t.Fatalf("session workspace %q is outside the test's temp dir",
			record.Metadata.WorkspacePath)
	}
	if os.Getenv("AO_DATA_DIR") != "" {
		t.Fatal("the fixture set AO_DATA_DIR; it must need no data-dir override")
	}
}

// isUnderTempDir reports whether path is rooted in the test binary's temp directory.
func isUnderTempDir(t *testing.T, path string) bool {
	t.Helper()
	root, err := filepath.Abs(os.TempDir())
	if err != nil {
		t.Fatalf("resolve temp dir: %v", err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("resolve %q: %v", path, err)
	}
	rel, err := filepath.Rel(root, absolute)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
