package sessionmanager

import (
	"context"
	"errors"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type preparedImportAgent struct {
	terminalImportAgent
	ref     ports.NativeSessionRef
	err     error
	calls   int
	request ports.ImportedSessionRelocation
}

func (a *preparedImportAgent) PrepareImportedSession(_ context.Context, request ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	a.calls++
	a.request = request
	return a.ref, a.err
}

func TestImportedNativePreparationCheckpointsIdentityBeforeLaunch(t *testing.T) {
	a := &preparedImportAgent{ref: ports.NativeSessionRef{NativeSessionID: "copied-native", ConfigDir: t.TempDir(), TranscriptPath: "/private/copy.json"}}
	m, st, _ := newExitedResumeManager(t, &fakeRuntime{}, a)
	rec := st.sessions["mer-1"]
	rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "original", ConfigDir: t.TempDir(), Prepared: true, Fingerprint: "synthetic-native-snapshot"}
	rec.Metadata.NativeTranscriptPath = "/original/history.json"
	st.sessions[rec.ID] = rec
	prepared, err := m.relocateImportedSession(context.Background(), rec, rec.Metadata.WorkspacePath, nil)
	if err != nil {
		t.Fatal(err)
	}
	source := prepared.Metadata.ImportSource
	if a.request.SourceFingerprint != "synthetic-native-snapshot" {
		t.Fatal("native preparer did not receive the selected source fingerprint")
	}
	if prepared.Metadata.AgentSessionID != "copied-native" || prepared.Metadata.ProviderConversationID != "copied-native" || source.NativeID != "original" || source.ResumeNativeID != "copied-native" || prepared.Metadata.NativeTranscriptPath != "/private/copy.json" {
		t.Fatalf("prepared identity = %#v", prepared.Metadata)
	}
	checkpoint := st.sessions[rec.ID]
	if checkpoint.Metadata.ImportSource.ResumeNativeID != "copied-native" || checkpoint.Metadata.AgentSessionID != rec.Metadata.AgentSessionID {
		t.Fatalf("prelaunch checkpoint = %#v", checkpoint.Metadata)
	}
	a.err = errors.New("preparation must not run again")
	again, err := m.relocateImportedSession(context.Background(), checkpoint, rec.Metadata.WorkspacePath, nil)
	if err != nil || a.calls != 1 || again.Metadata.AgentSessionID != "copied-native" {
		t.Fatalf("retry = %#v, calls=%d, %v", again.Metadata, a.calls, err)
	}
	env := map[string]string{}
	m.applyImportedConfigEnv(context.Background(), again, env)
	if env["CODEX_HOME"] != a.ref.ConfigDir {
		t.Fatalf("prepared native root = %v", env)
	}
}

func TestImportedNativePreparationRejectsUnidentifiedConversation(t *testing.T) {
	for _, nativeID := range []string{"", " native "} {
		a := &preparedImportAgent{ref: ports.NativeSessionRef{NativeSessionID: nativeID}}
		rt := &fakeRuntime{}
		m, st, _ := newExitedResumeManager(t, rt, a)
		rec := st.sessions["mer-1"]
		rec.Metadata.ImportSource = &domain.SessionImportSource{NativeID: "original", ConfigDir: t.TempDir(), Prepared: true}
		st.sessions[rec.ID] = rec
		if _, err := m.relocateImportedSession(context.Background(), rec, rec.Metadata.WorkspacePath, nil); err == nil {
			t.Fatalf("accepted unidentified native id %q", nativeID)
		}
		if st.sessions[rec.ID].Metadata.ImportSource.ResumeNativeID != "" || rt.created != 0 {
			t.Fatal("failed preparation published a resume identity or workspace")
		}
	}
}
