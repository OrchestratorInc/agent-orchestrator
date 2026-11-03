package sessionmanager

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
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
	next.ResumeTranscriptPath = ""
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

type preparedInteractiveImportAgent struct {
	importInteractiveAgent
	preparations int
	initializers int
}

func (a *preparedInteractiveImportAgent) PrepareImportedSession(_ context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	a.preparations++
	return ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: r.ConfigDir}, nil
}

func (a *preparedInteractiveImportAgent) PrepareNativeRestore(ctx context.Context, cfg ports.RestoreConfig) (ports.NativeRestoreInitialization, error) {
	a.initializers++
	return a.importInteractiveAgent.PrepareNativeRestore(ctx, cfg)
}

type failingNativePublicationLCM struct {
	*nativeBindingLCM
	fail bool
}

type locatedForkAgent struct {
	importInteractiveAgent
	found bool
	path  string
}

func (a *locatedForkAgent) LocateTranscript(_ context.Context, ref ports.NativeSessionRef) (string, bool, error) {
	if ref.NativeSessionID != "fork-native" {
		return "", false, errors.New("lookup used the source id instead of the fork")
	}
	return a.path, a.found, nil
}

func TestInteractiveForkBindsObservedTranscriptAndRefusesMissingBinding(t *testing.T) {
	for _, found := range []bool{false, true} {
		t.Run(fmt.Sprintf("found-%v", found), func(t *testing.T) {
			m, st, _, _ := interactiveManager(t, false, &interactiveTestAgent{plan: &interactiveTestPlan{}})
			root := t.TempDir()
			a := &locatedForkAgent{found: found, path: filepath.Join(root, "fork-native.jsonl"), importInteractiveAgent: importInteractiveAgent{plan: &forkInteractivePlan{source: "agent-x", importInteractivePlan: &importInteractivePlan{target: "fork-native"}}}}
			m.agents = singleAgent{agent: a}
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
			rec.Metadata.WorkspacePath = t.TempDir()
			rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID, rec.Metadata.AgentSessionIDLaunchID = "", "", ""
			rec.Metadata.ProviderConversationID = "agent-x"
			rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "agent-x", ConfigDir: root, Transferred: true, Prepared: true}
			st.sessions[rec.ID] = rec
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if found {
				if err != nil || result.Session.Metadata.NativeTranscriptPath != a.path || result.Session.NeedsImportResume() {
					t.Fatalf("fork transcript binding = %#v, %v", result, err)
				}
			} else if err == nil || !st.sessions[rec.ID].NeedsImportResume() || st.sessions[rec.ID].Metadata.AgentSessionID != "agent-x" {
				t.Fatalf("missing fork binding accepted: %#v, %v", result, err)
			}
		})
	}
}

func (l *failingNativePublicationLCM) ApplyActivitySignal(ctx context.Context, id domain.SessionID, signal ports.ActivitySignal) error {
	// Fail publication only; rollback must still be able to record process exit.
	if l.fail && signal.Event == "session-start" {
		return errors.New("synthetic identity publication failure")
	}
	return l.nativeBindingLCM.ApplyActivitySignal(ctx, id, signal)
}

func TestPreparedInteractiveImportRetriesFailedNativeInitialization(t *testing.T) {
	for _, observedFork := range []bool{false, true} {
		t.Run(fmt.Sprintf("observed-fork-%v", observedFork), func(t *testing.T) {
			m, st, rt, msg := interactiveManager(t, false, &interactiveTestAgent{plan: &interactiveTestPlan{}})
			generation := 0
			m.newLaunchID = func() string {
				generation++
				return fmt.Sprintf("retry-generation-%d", generation)
			}
			first := &importInteractivePlan{target: "agent-x"}
			a := &preparedInteractiveImportAgent{importInteractiveAgent: importInteractiveAgent{plan: first}}
			publication := &failingNativePublicationLCM{nativeBindingLCM: &nativeBindingLCM{fakeLCM: &fakeLCM{store: st}}}
			m.agents, m.lcm = singleAgent{agent: a}, publication
			if observedFork {
				first.target = "observed-fork"
				a.plan = &forkInteractivePlan{importInteractivePlan: first, source: "agent-x"}
				publication.fail = true
			} else {
				first.ready = func(context.Context) (bool, error) { return false, errors.New("synthetic initialization failure") }
			}
			rec := st.sessions["mer-1"]
			rec.Mode, rec.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
			rec.Metadata.WorkspacePath = t.TempDir()
			rec.Metadata.RuntimeHandleID, rec.Metadata.RuntimeLaunchID, rec.Metadata.AgentSessionIDLaunchID = "", "", ""
			rec.Metadata.ProviderConversationID = "agent-x"
			rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "agent-x", ConfigDir: t.TempDir(), Transferred: true, Prepared: true}
			st.sessions[rec.ID] = rec
			if _, err := m.ResumeAgentWithMode(context.Background(), rec.ID); err == nil {
				t.Fatal("first native initialization unexpectedly succeeded")
			}
			failed := st.sessions[rec.ID]
			if !failed.NeedsImportResume() || failed.Activity.State != domain.ActivityExited || failed.Metadata.RuntimeLaunchID == "" {
				t.Fatalf("failure lost resumable import state: %#v", failed)
			}
			if rt.exitedLaunches == nil {
				rt.exitedLaunches = map[string]bool{}
			}
			rt.exitedLaunches[failed.Metadata.RuntimeLaunchID] = true
			publication.fail = false
			a.plan = &importInteractivePlan{target: failed.Metadata.ImportSource.ResumeNativeID}
			result, err := m.ResumeAgentWithMode(context.Background(), rec.ID)
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() || result.Session.Metadata.AgentSessionID != failed.Metadata.ImportSource.ResumeNativeID {
				t.Fatalf("prepared native retry = %#v, %v", result, err)
			}
			if a.preparations != 1 || a.initializers != 2 || len(msg.msgs) != 0 {
				t.Fatalf("retry recopied, bypassed initialization or sent a task: prepares=%d initializers=%d messages=%v", a.preparations, a.initializers, msg.msgs)
			}
		})
	}
}
