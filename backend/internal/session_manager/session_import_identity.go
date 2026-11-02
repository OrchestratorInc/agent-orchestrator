package sessionmanager

import (
	"context"
	"errors"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func importedNativeRef(source *domain.SessionImportSource) ports.NativeSessionRef {
	ref := ports.NativeSessionRef{NativeSessionID: source.NativeID, ConfigDir: source.ConfigDir}
	if source.ResumeNativeID != "" {
		ref.NativeSessionID = source.ResumeNativeID
	}
	if source.ResumeConfigDir != "" {
		ref.ConfigDir = source.ResumeConfigDir
	}
	ref.TranscriptPath = source.ResumeTranscriptPath
	return ref
}

func (m *Manager) prepareImportedNativeSession(ctx context.Context, rec domain.SessionRecord, launchPath string, preparer ports.AgentImportedSessionPreparer) (domain.SessionRecord, error) {
	source := rec.Metadata.ImportSource
	if source.ResumeNativeID == "" {
		ref, err := preparer.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{
			NativeSessionID: source.NativeID, ConfigDir: source.ConfigDir,
			TranscriptPath: rec.Metadata.NativeTranscriptPath, SourceCWD: source.CWD, LaunchCWD: launchPath,
		})
		if err != nil {
			return rec, err
		}
		if strings.TrimSpace(ref.NativeSessionID) == "" || ref.NativeSessionID != strings.TrimSpace(ref.NativeSessionID) {
			return rec, errors.New("import preparation did not identify a native conversation")
		}
		next := *source
		next.ResumeNativeID, next.ResumeConfigDir, next.ResumeCwd = ref.NativeSessionID, ref.ConfigDir, launchPath
		next.ResumeTranscriptPath = ref.TranscriptPath
		if next.SourceTranscriptPath == "" {
			next.SourceTranscriptPath = rec.Metadata.NativeTranscriptPath
		}
		updated, err := m.store.SetSessionImportWorkspace(ctx, rec.ID, source, &next, rec.Metadata, m.clock())
		if err != nil {
			return rec, err
		}
		if !updated {
			return rec, importNotice("session changed while preparing the native conversation; retry")
		}
		rec.Metadata.ImportSource = &next
	}
	ref := importedNativeRef(rec.Metadata.ImportSource)
	rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = ref.NativeSessionID, ref.NativeSessionID
	rec.Metadata.AgentSessionIDLaunchID = ""
	if ref.TranscriptPath != "" {
		rec.Metadata.NativeTranscriptPath = ref.TranscriptPath
	}
	return rec, nil
}

func nativeRestoreMetadata(rec domain.SessionRecord) map[string]string {
	metadata := map[string]string{ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID}
	if rec.NeedsImportResume() {
		source := rec.Metadata.ImportSource
		metadata[ports.MetadataKeyImportSourceNativeID] = source.NativeID
		metadata[ports.MetadataKeyImportResumeNativeID] = source.ResumeNativeID
		metadata[ports.MetadataKeyImportConfigDir] = importedNativeRef(source).ConfigDir
	}
	return metadata
}

type importedIdentityStore interface {
	CommitImportedNativeSessionID(context.Context, domain.SessionID, *domain.SessionImportSource, string, string, string) (bool, error)
}
