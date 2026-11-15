package ports

type TerminalSurfaceWorkState uint8

const (
	TerminalSurfaceWorkUnknown TerminalSurfaceWorkState = iota
	TerminalSurfaceWorkActive
	TerminalSurfaceWorkIdle
	TerminalSurfaceWorkWaitingInput
	TerminalSurfaceWorkBlocked
)

type TerminalComposerState uint8

const (
	TerminalComposerUnknown TerminalComposerState = iota
	TerminalComposerEmpty
	TerminalComposerDraft
)

type TerminalSurfaceObservation struct {
	Work                         TerminalSurfaceWorkState
	Composer                     TerminalComposerState
	NativeConversationNotStarted bool
}

type TerminalSurfaceInspector interface {
	InspectTerminalSurface(output string) TerminalSurfaceObservation
}
