package opencodeacp

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/kimi"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/omp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/opencode"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/claudeacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/kimiacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/ompacp"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/persistenthost"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Opt-in account-backed verification, like TestLiveOpenCodeACP. CI sends no
// prompts; run AO_LIVE_NATIVE_FORKS=1 go test -run TestLiveNativeACPForks -v.
func TestLiveNativeACPForks(t *testing.T) {
	if os.Getenv("AO_LIVE_NATIVE_FORKS") != "1" {
		t.Skip("set AO_LIVE_NATIVE_FORKS=1 to verify installed authenticated ACP providers")
	}
	drivers := []ports.ChatDriver{
		claudeacp.New(claudecode.New(), nil, nil),
		New(opencode.New(), nil),
		kimiacp.New(kimi.New(), nil),
		ompacp.New(omp.New(), nil),
	}
	for _, driver := range drivers {
		t.Run(string(driver.Harness()), func(t *testing.T) {
			t.Parallel()
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
			defer cancel()
			if _, err := driver.Probe(ctx); err != nil {
				t.Fatal(err)
			}
			cfg := ports.ChatStartConfig{
				SessionID: domain.SessionID("fork-parent-" + uuid.NewString()), DataDir: t.TempDir(), WorkspacePath: t.TempDir(),
				Permissions: ports.PermissionModeDefault, Env: envMap(),
			}
			cfg.Env["AO_SESSION_ID"] = string(cfg.SessionID)
			cfg.Env["AO_SESSION"] = string(cfg.SessionID)
			cfg.Env["AO_DATA_DIR"] = cfg.DataDir
			delete(cfg.Env, "AO_BROWSER_CAPABILITY")
			delete(cfg.Env, "AO_PREVIEW_CAPABILITY")
			parent, err := driver.Start(ctx, cfg)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = parent.(ports.ChatProviderTerminator).Terminate() })
			if !parent.Capabilities().Has(ports.ChatCapabilityForkCurrent) {
				t.Fatal("provider did not advertise current-context forks")
			}
			sourceMarker, childMarker, laterMarker := "AO-SOURCE-"+uuid.NewString(), "AO-CHILD-"+uuid.NewString(), "AO-LATER-"+uuid.NewString()
			seedAnswer, _, _ := sendLiveOpenCodeTurn(ctx, t, parent, uuid.NewString(), "Protocol test: do not use tools, commands, or files. Remember the synthetic marker "+sourceMarker+". Reply only OK.")
			if !strings.Contains(seedAnswer, "OK") {
				t.Fatalf("parent could not complete the seed prompt before forking: %q", seedAnswer)
			}
			childCfg := cfg
			childCfg.Env = envMap()
			childCfg.SessionID = domain.SessionID("fork-child-" + uuid.NewString())
			childCfg.ProviderScopeID = "side-" + uuid.NewString()
			childCfg.Env["AO_SESSION_ID"] = string(childCfg.SessionID)
			childCfg.Env["AO_SESSION"] = string(childCfg.SessionID)
			childCfg.Env["AO_DATA_DIR"] = cfg.DataDir
			delete(childCfg.Env, "AO_BROWSER_CAPABILITY")
			delete(childCfg.Env, "AO_PREVIEW_CAPABILITY")
			t.Cleanup(func() { _ = persistenthost.Shutdown(context.Background(), cfg.DataDir, string(childCfg.SessionID)) })
			child, err := driver.(ports.ChatIsolatedForker).ForkIntoHost(ctx, parent.ProviderConversationID(), "", childCfg)
			if err != nil {
				t.Fatal(err)
			}
			childID := child.ProviderConversationID()
			if childID == "" || childID == parent.ProviderConversationID() {
				t.Fatal("fork did not return an independent identity")
			}
			t.Logf("native fork created for %s", driver.Harness())
			answer, _, _ := sendLiveOpenCodeTurn(ctx, t, child, uuid.NewString(), "Do not use tools. Remember the additional synthetic marker "+childMarker+". List every synthetic marker you have seen in this conversation, exactly, with no other text.")
			if !strings.Contains(answer, sourceMarker) || !strings.Contains(answer, childMarker) {
				t.Fatalf("child lost context: %q", answer)
			}
			if err := child.(ports.ChatProviderTerminator).Terminate(); err != nil {
				t.Fatal(err)
			}
			child, err = driver.Resume(ctx, ports.ChatResumeConfig{
				SessionID: childCfg.SessionID, ProviderConversationID: childID, DataDir: cfg.DataDir, WorkspacePath: cfg.WorkspacePath,
				Env: childCfg.Env, Permissions: cfg.Permissions, ProviderScopeID: childCfg.ProviderScopeID,
			})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = child.(ports.ChatProviderTerminator).Terminate() })
			answer, _, _ = sendLiveOpenCodeTurn(ctx, t, parent, uuid.NewString(), "Do not use tools. Remember the additional synthetic marker "+laterMarker+". List every synthetic marker you have seen in this conversation, exactly, with no other text.")
			if !strings.Contains(answer, sourceMarker) || !strings.Contains(answer, laterMarker) || strings.Contains(answer, childMarker) {
				t.Fatalf("child mutated parent context: %q", answer)
			}
			answer, _, _ = sendLiveOpenCodeTurn(ctx, t, child, uuid.NewString(), "Do not use tools. List every synthetic marker you have seen in this conversation, exactly, with no other text.")
			if !strings.Contains(answer, sourceMarker) || !strings.Contains(answer, childMarker) || strings.Contains(answer, laterMarker) {
				t.Fatalf("restarted child lost context or inherited later parent work: %q", answer)
			}
			if err := child.(ports.ChatProviderTerminator).Terminate(); err != nil {
				t.Fatal(err)
			}
			if err := parent.(ports.ChatForkDeleter).DeleteFork(ctx, childID); err != nil && !strings.Contains(err.Error(), "does not advertise session/delete") {
				t.Fatal(err)
			}
			t.Log("inherited context, parent/child independence, restart, and host cleanup verified")
		})
	}
}
