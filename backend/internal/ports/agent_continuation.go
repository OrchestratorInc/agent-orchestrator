package ports

import "context"

type FreshNativeSessionIDMode string

const (
	FreshNativeSessionIDProviderAssigned FreshNativeSessionIDMode = "provider_assigned"
	FreshNativeSessionIDCallerAssigned   FreshNativeSessionIDMode = "caller_assigned"
)

type ContinuationCapabilities struct {
	FreshNativeSessionID FreshNativeSessionIDMode
}

type AgentContinuationCapabilityProvider interface {
	ContinuationCapabilities() ContinuationCapabilities
}

type AgentFreshNativeSessionIDProvider interface {
	NewNativeSessionID() string
}

type NativeSessionRef struct {
	NativeSessionID string
	ConfigDir       string
	TranscriptPath  string
}

const MetadataKeyImportSourceNativeID = "importSourceNativeId"

const MetadataKeyImportResumeNativeID = "importResumeNativeId"

const MetadataKeyImportConfigDir = "importConfigDir"

type AgentNativeSessionConfigProvider interface {
	NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error)
}

type NativeSessionAvailability string

const (
	NativeSessionAvailabilityAvailable   NativeSessionAvailability = "available"
	NativeSessionAvailabilityUnavailable NativeSessionAvailability = "unavailable"
	NativeSessionAvailabilityUnknown     NativeSessionAvailability = "unknown"
)

type AgentNativeSessionProber interface {
	ProbeNativeSession(ctx context.Context, ref NativeSessionRef) (NativeSessionAvailability, error)
}

type AgentTranscriptLocator interface {
	LocateTranscript(ctx context.Context, ref NativeSessionRef) (path string, ok bool, err error)
}

type ImportedSessionRelocation struct {
	NativeSessionID   string
	ConfigDir         string
	TranscriptPath    string
	SourceCWD         string
	LaunchCWD         string
	Chat              bool
	SourceFingerprint string
	DataDir           string
	Env               map[string]string
}

type AgentImportedSessionRelocator interface {
	RelocateImportedSession(ctx context.Context, r ImportedSessionRelocation) error
}

type AgentImportedSessionPreparer interface {
	PrepareImportedSession(context.Context, ImportedSessionRelocation) (NativeSessionRef, error)
}
