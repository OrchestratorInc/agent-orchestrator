package sessionmanager

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type recoveryImportAgent struct {
	terminalImportAgent
	fingerprint  string
	sourceRoot   string
	calls        int
	sourceProbes int
}

func recoveryFingerprint(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (a *recoveryImportAgent) PrepareImportedSession(_ context.Context, r ports.ImportedSessionRelocation) (ports.NativeSessionRef, error) {
	a.calls++
	ref := ports.NativeSessionRef{NativeSessionID: r.NativeSessionID, ConfigDir: r.LaunchCWD, TranscriptPath: filepath.Join(r.LaunchCWD, "private-native")}
	if r.SourceFingerprint == "" || r.SourceFingerprint != a.fingerprint {
		return ref, errors.New("selection fingerprint is required")
	}
	proof := r.NativeSessionID + "\n" + r.TranscriptPath + "\n" + r.LaunchCWD + "\n" + r.SourceFingerprint
	if data, err := os.ReadFile(ref.TranscriptPath); err == nil {
		marker, markerErr := os.ReadFile(ref.TranscriptPath + ".proof")
		if markerErr != nil || string(marker) != proof || recoveryFingerprint(data) != a.fingerprint {
			return ref, errors.New("private native copy is unproven")
		}
		return ref, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return ref, err
	}
	data, err := os.ReadFile(r.TranscriptPath)
	if err != nil {
		return ref, err
	}
	if recoveryFingerprint(data) != a.fingerprint {
		return ref, errors.New("source differs from selection")
	}
	if err := os.WriteFile(ref.TranscriptPath, data, 0o600); err != nil {
		return ref, err
	}
	if err := os.WriteFile(ref.TranscriptPath+".proof", []byte(proof), 0o600); err != nil {
		return ref, err
	}
	return ref, nil
}

func (a *recoveryImportAgent) ProbeNativeSession(_ context.Context, ref ports.NativeSessionRef) (ports.NativeSessionAvailability, error) {
	if ref.ConfigDir == a.sourceRoot {
		a.sourceProbes++
		if _, err := os.Stat(filepath.Join(a.sourceRoot, "source-native")); err != nil {
			return ports.NativeSessionAvailabilityUnavailable, nil //nolint:nilerr // The fake classifies unreadable history as unavailable.
		}
	} else {
		data, err := os.ReadFile(ref.TranscriptPath)
		if err != nil || recoveryFingerprint(data) != a.fingerprint {
			return ports.NativeSessionAvailabilityUnavailable, nil //nolint:nilerr // The fake classifies unreadable or changed history as unavailable.
		}
	}
	return ports.NativeSessionAvailabilityAvailable, nil
}

type recoveryArchiveRefresher struct {
	calls  int
	before func()
	err    error
}

func (a *recoveryArchiveRefresher) RefreshArchive(context.Context, domain.SessionRecord) error {
	a.calls++
	if a.before != nil {
		a.before()
	}
	return a.err
}

func seedRecoveryImport(t *testing.T, store *fakeStore, sourceRoot, fingerprint string) domain.SessionRecord {
	t.Helper()
	record := store.sessions["mer-1"]
	record.Mode, record.Activity.State = domain.SessionModeTUI, domain.ActivityIdle
	record.Metadata.WorkspacePath = t.TempDir()
	record.Metadata.RuntimeHandleID, record.Metadata.RuntimeLaunchID = "", ""
	record.Metadata.ProviderConversationID = record.Metadata.AgentSessionID
	record.Metadata.NativeTranscriptPath = filepath.Join(sourceRoot, "source-native")
	record.Metadata.ImportSource = &domain.SessionImportSource{NativeID: record.Metadata.AgentSessionID, ConfigDir: sourceRoot, CWD: sourceRoot, Fingerprint: fingerprint, Prepared: true, Transferred: true}
	store.sessions[record.ID] = record
	return record
}

func TestFingerprintNativeCopyRecoversAfterUncheckpointedPublication(t *testing.T) {
	for _, change := range []string{"removed", "changed"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			data := []byte("synthetic pinned native context")
			root := t.TempDir()
			if err := os.WriteFile(filepath.Join(root, "source-native"), data, 0o600); err != nil {
				t.Fatal(err)
			}
			agent := &recoveryImportAgent{terminalImportAgent: terminalImportAgent{native: true}, fingerprint: recoveryFingerprint(data), sourceRoot: root}
			runtime := &fakeRuntime{}
			manager, store, _ := newExitedResumeManager(t, runtime, agent)
			record := seedRecoveryImport(t, store, root, agent.fingerprint)
			manager.store = &failingImportWorkspaceStore{fakeStore: store, failAt: 1}
			archive := &recoveryArchiveRefresher{err: errors.New("original source must not be reread")}
			manager.SetImportArchiveRefresher(archive)
			if _, err := manager.ResumeAgentWithMode(ctx, record.ID); err == nil {
				t.Fatal("checkpoint failure was not reported")
			}
			privatePath := filepath.Join(record.Metadata.WorkspacePath, "private-native")
			published, err := os.ReadFile(privatePath)
			if err != nil || string(published) != string(data) || agent.calls != 1 || runtime.created != 0 {
				t.Fatal("native copy was not published before the injected checkpoint failure")
			}
			pending := store.sessions[record.ID].Metadata.ImportSource
			if pending.ResumeNativeID != "" || pending.ResumeCwd != "" {
				t.Fatal("failed checkpoint unexpectedly persisted a resume target")
			}
			if change == "removed" {
				if err := os.Remove(record.Metadata.NativeTranscriptPath); err != nil {
					t.Fatal(err)
				}
			} else if err := os.WriteFile(record.Metadata.NativeTranscriptPath, []byte("different original context"), 0o600); err != nil {
				t.Fatal(err)
			}
			// A new adapter instance must recover from durable private proof,
			// not process memory left over from the interrupted preparation.
			recovered := &recoveryImportAgent{terminalImportAgent: terminalImportAgent{native: true}, fingerprint: agent.fingerprint, sourceRoot: root}
			manager.agents = singleAgent{agent: recovered}
			result, err := manager.ResumeAgentWithMode(ctx, record.ID)
			if err != nil || result.Mode != RestoreModeNative || result.Session.NeedsImportResume() {
				t.Fatalf("recovery = %#v, %v", result, err)
			}
			if recovered.calls != 1 || recovered.sourceProbes != 0 || archive.calls != 0 || runtime.created != 1 {
				t.Fatal("recovery refreshed/probed the original or failed to launch the proven copy")
			}
			after, err := os.ReadFile(privatePath)
			if err != nil || string(after) != string(data) || result.Session.Metadata.NativeTranscriptPath != privatePath {
				t.Fatal("recovery changed or failed to pin the private native context")
			}
		})
	}
}

func TestFingerprintRecoveryRefusesAbsentOrUnprovenCopy(t *testing.T) {
	for _, scenario := range []string{"absent", "unproven", "changed source"} {
		t.Run(scenario, func(t *testing.T) {
			root := t.TempDir()
			data := []byte("synthetic pinned native context")
			agent := &recoveryImportAgent{terminalImportAgent: terminalImportAgent{native: true}, fingerprint: recoveryFingerprint(data), sourceRoot: root}
			runtime := &fakeRuntime{}
			manager, store, _ := newExitedResumeManager(t, runtime, agent)
			record := seedRecoveryImport(t, store, root, agent.fingerprint)
			if scenario == "changed source" {
				if err := os.WriteFile(record.Metadata.NativeTranscriptPath, []byte("different original context"), 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "unproven" {
				if err := os.WriteFile(filepath.Join(record.Metadata.WorkspacePath, "private-native"), data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := manager.ResumeAgentWithMode(context.Background(), record.ID); err == nil {
				t.Fatal("unproven native recovery was accepted")
			}
			if agent.calls != 1 || runtime.created != 0 || store.sessions[record.ID].Metadata.ImportSource.ResumeNativeID != "" {
				t.Fatal("unproven copy reached launch or was checkpointed")
			}
		})
	}
}

func TestUnpreparedWorkspaceStillRequiresNativeSourceAvailability(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "not prepared", true: "directory removed"}[missing], func(t *testing.T) {
			root := t.TempDir()
			agent := &recoveryImportAgent{fingerprint: "selected", sourceRoot: root}
			manager, store, _ := newExitedResumeManager(t, &fakeRuntime{}, agent)
			record := seedRecoveryImport(t, store, root, agent.fingerprint)
			if missing {
				if err := os.RemoveAll(record.Metadata.WorkspacePath); err != nil {
					t.Fatal(err)
				}
			} else {
				record.Metadata.ImportSource.Prepared = false
			}
			store.sessions[record.ID] = record
			if _, err := manager.ResumeAgentWithMode(context.Background(), record.ID); err == nil || agent.sourceProbes != 1 || agent.calls != 0 {
				t.Fatal("unprepared workspace bypassed the original native availability check")
			}
		})
	}
}

func TestUnfingerprintedPreparerKeepsRefreshBeforePreparation(t *testing.T) {
	agent := &preparedImportAgent{terminalImportAgent: terminalImportAgent{native: true}, ref: ports.NativeSessionRef{NativeSessionID: "agent-x", ConfigDir: t.TempDir()}}
	manager, store, _ := newExitedResumeManager(t, &fakeRuntime{}, agent)
	record := seedRecoveryImport(t, store, t.TempDir(), "")
	archive := &recoveryArchiveRefresher{before: func() {
		if agent.calls != 0 {
			t.Fatal("unfingerprinted import moved refresh after native preparation")
		}
	}}
	manager.SetImportArchiveRefresher(archive)
	if _, err := manager.ResumeAgentWithMode(context.Background(), record.ID); err != nil || archive.calls != 1 || agent.calls != 1 {
		t.Fatalf("ordinary preparation refresh changed: %v", err)
	}
}
