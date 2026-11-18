package sessionmanager

import (
	"context"
	"errors"
	"maps"
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

// Fingerprinted preparers verify the selected source or recover their proven
// private copy before checkpointing. Original-source reads must not prevent
// that recovery after publication succeeds but checkpoint persistence fails.
func (m *Manager) hasFingerprintImportPreparer(rec domain.SessionRecord) bool {
	if rec.Metadata.ImportSource == nil || rec.Metadata.ImportSource.Fingerprint == "" || m.agents == nil {
		return false
	}
	agent, ok := m.agents.Agent(rec.Harness)
	if !ok {
		return false
	}
	_, ok = agent.(ports.AgentImportedSessionPreparer)
	return ok
}

func (m *Manager) prepareImportedNativeSession(ctx context.Context, rec domain.SessionRecord, launchPath string, preparer ports.AgentImportedSessionPreparer, env map[string]string) (domain.SessionRecord, error) {
	source := rec.Metadata.ImportSource
	if source.ResumeNativeID == "" {
		ref, err := preparer.PrepareImportedSession(ctx, ports.ImportedSessionRelocation{
			NativeSessionID:   source.NativeID,
			ConfigDir:         source.ConfigDir,
			TranscriptPath:    rec.Metadata.NativeTranscriptPath,
			SourceCWD:         source.CWD,
			LaunchCWD:         launchPath,
			Chat:              domain.NormalizeSessionMode(rec.Mode) == domain.SessionModeChat,
			SourceFingerprint: source.Fingerprint,
			DataDir:           m.dataDir,
			Env:               maps.Clone(env),
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
		updated, err := m.store.SetPreparedImportIdentity(ctx, rec.ID, source, &next, rec.Metadata, m.clock())
		if err != nil {
			return rec, err
		}
		if !updated {
			return rec, importNotice("session changed while preparing the native conversation; retry")
		}
		rec.Metadata.ImportSource = &next
	}
	ref := importedNativeRef(rec.Metadata.ImportSource)
	if rec.Metadata.AgentSessionID != ref.NativeSessionID {
		rec.Metadata.AgentSessionIDLaunchID = ""
	}
	rec.Metadata.AgentSessionID, rec.Metadata.ProviderConversationID = ref.NativeSessionID, ref.NativeSessionID
	if ref.TranscriptPath != "" {
		rec.Metadata.NativeTranscriptPath = ref.TranscriptPath
	}
	return rec, nil
}

func nativeRestoreMetadata(rec domain.SessionRecord) map[string]string {
	metadata := map[string]string{ports.MetadataKeyAgentSessionID: rec.Metadata.AgentSessionID}
	if source := rec.Metadata.ImportSource; source != nil && rec.Metadata.AgentSessionID == importedNativeRef(source).NativeSessionID {
		metadata[ports.MetadataKeyImportSourceNativeID] = source.NativeID
		metadata[ports.MetadataKeyImportResumeNativeID] = source.ResumeNativeID
		metadata[ports.MetadataKeyImportConfigDir] = importedNativeRef(source).ConfigDir
	}
	return metadata
}
