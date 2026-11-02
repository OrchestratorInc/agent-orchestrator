package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type importInteractiveAgent struct {
	fakeAgent
	plan ports.NativeRestoreInitialization
}

func (a *importInteractiveAgent) PrepareNativeRestore(context.Context, ports.RestoreConfig) (ports.NativeRestoreInitialization, error) {
	return a.plan, nil
}

type importInteractivePlan struct {
	interactiveTestPlan
	target string
	input  string
}

func (p *importInteractivePlan) TargetID() string    { return p.target }
func (p *importInteractivePlan) ResumeInput() string { return p.input }

type forkInteractivePlan struct {
	*importInteractivePlan
	source string
}

func (p *forkInteractivePlan) SourceNativeSessionID() string { return p.source }

func (f *fakeStore) CommitImportedNativeSessionID(_ context.Context, id domain.SessionID, expected *domain.SessionImportSource, handleID, launchID, nativeID string) (bool, error) {
	rec := f.sessions[id]
	if expected == nil || !rec.NeedsImportResume() || rec.IsTerminated || *rec.Metadata.ImportSource != *expected || rec.Metadata.RuntimeHandleID != handleID || rec.Metadata.RuntimeLaunchID != launchID {
		return false, nil
	}
	next := *rec.Metadata.ImportSource
	next.ResumeNativeID = nativeID
	rec.Metadata.ImportSource = &next
	rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID, rec.Metadata.AgentSessionIDLaunchID = nativeID, nativeID, launchID
	f.sessions[id] = rec
	return true, nil
}

func TestTerminalImportInteractiveRestoreIsStrictAndTaskless(t *testing.T) {
	for _, name := range []string{"same identity no input", "observed fork", "unapproved fork", "wrong fork source", "empty identity", "unknown prompt", "prompt changed"} {
		t.Run(name, func(t *testing.T) {
			base := &importInteractivePlan{target: "agent-x"}
			var plan ports.NativeRestoreInitialization = base
			success := name == "same identity no input" || name == "observed fork"
			switch name {
			case "observed fork", "wrong fork source":
				base.target = "fork-native"
				source := "agent-x"
				if name == "wrong fork source" {
					source = "unrelated"
				}
				plan = &forkInteractivePlan{importInteractivePlan: base, source: source}
			case "unapproved fork":
				base.target = "fork-native"
			case "empty identity":
				base.target = ""
			case "unknown prompt":
				base.input = "n"
				base.ready = func(context.Context) (bool, error) { return false, errors.New("unrecognized native prompt") }
			case "prompt changed":
				base.input = "n"
				calls := 0
				base.ready = func(context.Context) (bool, error) {
					calls++
					if calls > 1 {
						base.input = "different question"
					}
					return true, nil
				}
			}
			m, st, rt, msg := interactiveManager(t, false, &interactiveTestAgent{plan: &interactiveTestPlan{}})
			m.agents = singleAgent{agent: &importInteractiveAgent{plan: plan}}
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
			rec.Metadata.WorkspacePath = t.TempDir()
			rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID, rec.Metadata.AgentSessionIDLaunchID = "", "", ""
			rec.Metadata.ProviderConversationID = "agent-x"
			rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "agent-x", ConfigDir: t.TempDir(), Transferred: true, Prepared: true}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if success {
				if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || result.Session.Metadata.AgentSessionID != base.target || result.Session.Metadata.ImportSource.NativeID != "agent-x" {
					t.Fatalf("native import = %#v, %v", result, err)
				}
				if name == "observed fork" && (result.Session.Metadata.ImportSource.ResumeNativeID != "fork-native" || result.Session.Metadata.ProviderConversationID != "fork-native") {
					t.Fatalf("fork was not adopted: %#v", result.Session.Metadata)
				}
			} else if err == nil || !st.sessions[rec.ID].NeedsImportResume() || rt.destroyed == 0 {
				t.Fatalf("unsafe restore accepted or left running: %#v, %v", result, err)
			}
			if len(msg.msgs) != 0 {
				t.Fatalf("unproven prompt or saved task sent: %v", msg.msgs)
			}
		})
	}
}
