package store_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/execution"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/intervention"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// TestM2ZeroProviderGuarantees is the runtime half of the M2-08 zero-provider proof.
//
// The static half is a grep over the new suite (no exec, no network, no provider paths) and
// an import-closure check over the M2 production packages. Grep proves what the code says;
// this test proves what it DOES, which is the part a reviewer cannot take on faith:
//
//  1. every user-data root is redirected to a brand-new empty directory, so ANY attempt to
//     read or write ~/.ao, ~/.codex, ~/.claude, or an XDG config dir would land in a
//     directory that starts empty;
//  2. the whole bind → intervene → message flow runs;
//  3. all three roots are then asserted to be STILL empty — no config was read into a cache,
//     no credential was written, no harness state was persisted;
//  4. the gateway that served the flow is asserted to be *execution.FakeGateway, and its
//     call counters are asserted to equal exactly the calls the test itself caused, so there
//     is no hidden traffic to any other gateway.
//
// What this does NOT prove: that a real provider would behave the same way. Nothing offline
// can prove that. It proves the M2 stack has no provider dependency at all.
func TestM2ZeroProviderGuarantees(t *testing.T) {
	// Redirect every root a provider adapter might consult. t.Setenv restores these after
	// the test, and it also makes them read-only to parallel tests, which is why this test
	// is deliberately not parallel.
	home := t.TempDir()
	xdgConfig := t.TempDir()
	xdgData := t.TempDir()
	xdgCache := t.TempDir()
	userProfile := t.TempDir()
	for _, pair := range [][2]string{
		{"HOME", home},
		{"USERPROFILE", userProfile},
		{"XDG_CONFIG_HOME", xdgConfig},
		{"XDG_DATA_HOME", xdgData},
		{"XDG_CACHE_HOME", xdgCache},
	} {
		t.Setenv(pair[0], pair[1])
	}

	ctx := context.Background()
	env := newAnomalyEnv(t, "zero-provider", ports.SessionReadinessReady)

	// --- the gateway is the fake, and only the fake --------------------------------
	var gateway ports.ExecutionGateway = env.gateway
	if _, ok := gateway.(*execution.FakeGateway); !ok {
		t.Fatalf("the flow was served by %T, not the offline fake", gateway)
	}

	// --- run the whole stack -------------------------------------------------------
	bound := env.bind(t)
	if bound.SessionID == nil {
		t.Fatal("the fixture did not bind")
	}
	interventions := env.interventionCoordinator()
	// A steer must join the turn the caller names, so the fake's default steer receipt is
	// used as-is: it echoes the requested turn and names an AO turn row.
	if _, err := interventions.Steer(sqlitetest.ExecutionContext(ctx, t, env.store, env.attempt.ID, e2eNow()), intervention.SteerRequest{
		WorkflowID: env.workflowID, TaskID: env.taskID, AttemptID: env.attempt.ID,
		ClientMessageID: "msg-zero-provider", Text: "steer",
		TargetProviderTurnID: "prov-turn-1", Harness: domain.HarnessCodex,
	}); err != nil {
		t.Fatalf("Steer: %v", err)
	}
	env.gateway.InterruptResult(ports.InterruptReceipt{Confirmed: true})
	if _, _, err := interventions.Stop(sqlitetest.ExecutionContext(ctx, t, env.store, env.attempt.ID, e2eNow()), intervention.StopRequest{
		WorkflowID: env.workflowID, TaskID: env.taskID, AttemptID: env.attempt.ID,
		ClientMessageID: "msg-zero-provider-stop",
	}); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	// --- no call reached anything but the recorded fake surface --------------------
	if got := env.gateway.SpawnCalls(); got != 1 {
		t.Fatalf("spawn calls = %d, want exactly 1", got)
	}
	if got := env.gateway.SteerCalls(); got != 1 {
		t.Fatalf("steer calls = %d, want exactly 1", got)
	}
	if got := env.gateway.SendCalls(); got != 0 {
		t.Fatalf("send calls = %d, want 0: the steer must not degrade to a send", got)
	}
	if got := env.gateway.InterruptCount(); got != 1 {
		t.Fatalf("interrupt calls = %d, want exactly 1", got)
	}

	// --- every user-data root is still empty --------------------------------------
	for _, root := range []struct{ name, path string }{
		{"HOME", home},
		{"USERPROFILE", userProfile},
		{"XDG_CONFIG_HOME", xdgConfig},
		{"XDG_DATA_HOME", xdgData},
		{"XDG_CACHE_HOME", xdgCache},
	} {
		entries, err := os.ReadDir(root.path)
		if err != nil {
			t.Fatalf("read %s: %v", root.name, err)
		}
		if len(entries) != 0 {
			names := make([]string, 0, len(entries))
			for _, entry := range entries {
				names = append(names, filepath.Join(root.path, entry.Name()))
			}
			t.Fatalf("%s was written to during an offline flow: %v", root.name, names)
		}
	}

	// Belt and braces: the provider config directories that would appear underneath those
	// roots if an adapter had run are called out explicitly, so the failure message names
	// the culprit instead of listing a temp path.
	for _, path := range []string{
		filepath.Join(home, ".ao"),
		filepath.Join(home, ".codex"),
		filepath.Join(home, ".claude"),
		filepath.Join(userProfile, ".ao"),
		filepath.Join(userProfile, ".codex"),
		filepath.Join(userProfile, ".claude"),
	} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatalf("%s exists after an offline flow; a provider path was consulted", path)
		}
	}
}
