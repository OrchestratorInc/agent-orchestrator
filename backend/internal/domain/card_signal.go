package domain

// CardSignalKind says what a CardSignal reports about a session's work.
type CardSignalKind string

const (
	// CardSignalFirst asks for the first summary, built from the task prompt.
	CardSignalFirst CardSignalKind = "first"
	// CardSignalActivity is one thing the agent did: a command, an edit, a failure.
	CardSignalActivity CardSignalKind = "activity"
	// CardSignalAssistant is the text of an assistant message.
	CardSignalAssistant CardSignalKind = "assistant"
	// CardSignalTurnStarted marks the beginning of an agent turn.
	CardSignalTurnStarted CardSignalKind = "turn_started"
	// CardSignalTurnCompleted marks the end of an agent turn.
	CardSignalTurnCompleted CardSignalKind = "turn_completed"
)

// CardSignal is the evidence the card-summary scheduler works from. The chat
// controller and the terminal hooks emit one per relevant event; none of them
// touch the agent's turn.
type CardSignal struct {
	Kind CardSignalKind
	// Text is the activity label or the assistant text.
	Text string
	// Stopped marks a TurnCompleted for a turn that failed or was interrupted,
	// so the card does not claim the agent finished.
	Stopped bool
}
