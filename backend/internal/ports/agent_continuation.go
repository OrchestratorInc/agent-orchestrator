package ports

import "context"

// FreshNativeSessionIDMode describes who chooses a new provider conversation's
// native identity.
type FreshNativeSessionIDMode string

const (
	// FreshNativeSessionIDProviderAssigned means the provider chooses the id and
	// AO learns it from the provider's lifecycle hooks.
	FreshNativeSessionIDProviderAssigned FreshNativeSessionIDMode = "provider_assigned"
	// FreshNativeSessionIDCallerAssigned means the adapter accepts
	// LaunchConfig.NativeSessionID for a fresh conversation.
	FreshNativeSessionIDCallerAssigned FreshNativeSessionIDMode = "caller_assigned"
)

// ContinuationCapabilities is the static fresh-conversation behavior AO needs
// before it activates an agent as part of a session switch. Runtime evidence
// such as whether one particular native session still exists is reported by
// AgentNativeSessionProber instead.
type ContinuationCapabilities struct {
	FreshNativeSessionID FreshNativeSessionIDMode
}

// AgentContinuationCapabilityProvider is implemented by adapters whose
// fresh native-session behavior has been verified. Its absence is
// intentionally treated as unsupported rather than assuming a provider
// accepts caller-selected native ids.
type AgentContinuationCapabilityProvider interface {
	ContinuationCapabilities() ContinuationCapabilities
}

// AgentFreshNativeSessionIDProvider keeps provider-specific id formats out of
// switch orchestration. AO persists the returned id and passes it back through
// LaunchConfig.NativeSessionID.
type AgentFreshNativeSessionIDProvider interface {
	NewNativeSessionID() string
}

// NativeSessionRef identifies provider-owned local session state without
// conflating it with the stable AO session id. ConfigDir is the exact native
// configuration/state root used by that invocation (for example CODEX_HOME).
type NativeSessionRef struct {
	NativeSessionID string
	ConfigDir       string
}

// AgentNativeSessionConfigProvider resolves the provider state root used for
// a launch. AO stores the returned path with the native-session binding so a
// later probe does not accidentally inspect another account/config home.
type AgentNativeSessionConfigProvider interface {
	NativeSessionConfigDir(ctx context.Context, env map[string]string) (string, error)
}

// NativeSessionAvailability is a tri-state result. Unknown is materially
// different from unavailable: some providers can resume remote conversations
// even when AO cannot find authoritative local state.
type NativeSessionAvailability string

const (
	// NativeSessionAvailabilityAvailable means authoritative backing state exists.
	NativeSessionAvailabilityAvailable NativeSessionAvailability = "available"
	// NativeSessionAvailabilityUnavailable means authoritative state is absent.
	NativeSessionAvailabilityUnavailable NativeSessionAvailability = "unavailable"
	// NativeSessionAvailabilityUnknown means the adapter cannot prove either case.
	NativeSessionAvailabilityUnknown NativeSessionAvailability = "unknown"
)

// AgentNativeSessionProber is an optional adapter capability for checking
// whether a stored provider-native resume handle still has usable backing
// state. A probe must return Unknown when its evidence is not authoritative.
type AgentNativeSessionProber interface {
	ProbeNativeSession(ctx context.Context, ref NativeSessionRef) (NativeSessionAvailability, error)
}

// AgentTranscriptLocator is an optional adapter capability for finding the
// provider-owned transcript associated with a native session. ok=false means
// no readable transcript was found; it does not by itself prove the native
// conversation cannot be resumed.
type AgentTranscriptLocator interface {
	LocateTranscript(ctx context.Context, ref NativeSessionRef) (path string, ok bool, err error)
}

// AgentNativeSessionResumePreparer makes a provider-owned conversation resumable
// from the AO worktree. Some harnesses (Gemini/Pi forks such as Qwen, OMP and
// Kimchi) locate a chat by the folder the agent runs in, so AO cannot resume an
// imported chat from its own worktree until the native transcript is copied to
// where the provider will look for cwd. The implementation:
//
//   - copies its own source transcript (found via AgentTranscriptLocator) to the
//     native lookup path for cwd, rewriting any embedded folder paths, and never
//     modifies the original;
//   - is idempotent and first-open-only: it writes tmp+rename at mode 0600 and,
//     when the target already exists, returns that path without overwriting it,
//     because after the first resume the copy holds AO's own new messages;
//   - decides for itself whether to resolve cwd's symlinks, matching exactly how
//     its provider encodes the folder name.
//
// cwd is the raw launch working directory the provider will run in this open.
// priorCwd is the cwd a previous open already prepared for ("" on the first
// open): when the launch cwd drifts (a later Setup can move it into a gitignored
// subdirectory), the adapter re-keys by copying from the prior copy — which
// holds AO's appended turns — rather than from the untouched original or the
// newest file on disk. The returned path is where the copy now lives, so the
// caller can confirm it landed at the provider's lookup path.
type AgentNativeSessionResumePreparer interface {
	PrepareNativeSessionResume(ctx context.Context, ref NativeSessionRef, cwd, priorCwd string) (preparedPath string, err error)
}
