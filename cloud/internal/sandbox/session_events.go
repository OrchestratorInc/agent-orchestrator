package sandbox

import "context"

// SessionEventRecorder appends an event to the event stream of the session
// whose sandbox a provider call acts on. It must not block for long.
type SessionEventRecorder func(ctx context.Context, eventType string, payload any)

type sessionEventRecorderKey struct{}

// WithSessionEventRecorder returns a context whose provider calls can append
// events to the session's stream (a provider Delete reports the project's
// Cleanup steps this way, while the session's sandbox is being deleted).
func WithSessionEventRecorder(ctx context.Context, recorder SessionEventRecorder) context.Context {
	return context.WithValue(ctx, sessionEventRecorderKey{}, recorder)
}

// RecordSessionEvent appends an event through the context's recorder; it
// does nothing when none is installed.
func RecordSessionEvent(ctx context.Context, eventType string, payload any) {
	if recorder, _ := ctx.Value(sessionEventRecorderKey{}).(SessionEventRecorder); recorder != nil {
		recorder(ctx, eventType, payload)
	}
}
