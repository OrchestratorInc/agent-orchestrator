package ports

import "context"

// NativeCheckpointVerifier resolves owned hook observations against provider
// ancestry after the source controller has conclusively stopped. An unresolved
// witness returns ErrChatHistoryUnsettled, never an inferred text-only boundary.
type NativeCheckpointVerifier interface {
	VerifyNativeCheckpoint(context.Context, NativeCheckpointRequest) (NativeCheckpointBoundary, error)
}

// NativeCheckpointRequest uses the same provider identity/config as resume.
type NativeCheckpointRequest struct {
	ProviderConversationID string
	Env                    map[string]string
	Evidence               string
}

// NativeCheckpointBoundary must match one completed replay turn, including its
// native user UUID. It does not replace AO's durable timeline high-water gate.
type NativeCheckpointBoundary struct {
	UserMessageID string
	UserText      string
	AssistantText string
	// Interrupted is a boundary turn the user interrupted (Esc). It has no
	// answer, so its replayed text is not compared: what was cut short, and
	// any filler the provider adds after the interrupt, varies with timing.
	Interrupted bool
}

// UnansweredNativeTailError is unsettled only because the newest turn has no
// reply. The provider can still write one just after it stops, so Boundary
// holds once the settle window has passed without one.
type UnansweredNativeTailError struct{ Boundary NativeCheckpointBoundary }

func (e *UnansweredNativeTailError) Error() string {
	return "native tail has no reply: " + e.Unwrap().Error()
}

func (e *UnansweredNativeTailError) Unwrap() error {
	return &ChatHistoryUnsettledError{Dimensions: []ChatHistoryMismatchDimension{ChatHistoryMismatchUnsettledBoundary}}
}
