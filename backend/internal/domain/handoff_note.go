package domain

import (
	"fmt"
	"regexp"
	"runtime"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	// HandoffNotePrefix opens every handoff note.
	HandoffNotePrefix = "<ao-handoff-request"

	// TerminalTurnRetention is how many Terminal UI turns AO keeps per session.
	TerminalTurnRetention = 50

	handoffPreviousTurnLimit = 5
	handoffOlderTurnLimit    = 15

	handoffLatestTurnBudget   = 10_000
	handoffLatestUserCap      = 3_000
	handoffPreviousTurnBudget = 2_000
	handoffPreviousUserCap    = 600
	handoffOlderTurnBudget    = 350
	handoffOlderUserCap       = 150

	handoffOriginalTaskCap  = 2_000
	handoffChangedFileLimit = 50
	handoffChangedFileCap   = 160
	handoffHistoryPathCap   = 400

	// A note delivered as a launch argument must also fit the operating
	// system's per-argument limit, which is counted in bytes.
	handoffNoteMaxBytes = 96_000

	handoffClipMarker     = "\n…[clipped]…\n"
	handoffLineClipMarker = " …[clipped]… "
)

// TerminalTurn is a Terminal UI turn's request as the prompt hook reported it.
type TerminalTurn struct {
	SessionID       SessionID
	Harness         AgentHarness
	NativeSessionID string
	UserRequest     string
	RequestedAt     time.Time
}

// HandoffTurn is one turn of a session in the form the handoff note and the
// history command share, whichever UI it happened in.
type HandoffTurn struct {
	UserRequest   string       `json:"userRequest"`
	AgentResponse string       `json:"agentResponse"`
	At            time.Time    `json:"at"`
	Harness       AgentHarness `json:"harness,omitempty"`
}

// HandoffNoteInput is everything a handoff note is rendered from. Turns holds
// every turn AO knows about, oldest first; the builder decides what fits.
type HandoffNoteInput struct {
	SwitchID      string
	SourceHarness AgentHarness
	TargetHarness AgentHarness
	SessionTitle  string
	Project       string
	WorkspacePath string
	Branch        string
	At            time.Time
	OriginalTask  string
	ChangedFiles  []string
	Turns         []HandoffTurn
	Terminal      bool
	// HistoryFiles point at the agent's own record for an agent whose answers
	// AO did not record, because it reports no turns through hooks.
	HistoryFiles []HandoffHistoryFile
}

// HandoffHistoryFile is one agent's own record of this session and how to read it.
type HandoffHistoryFile struct {
	Harness AgentHarness
	Path    string
	// Read says how to find the messages in the file.
	Read string
	// RequestsOnly marks an agent whose requests AO recorded but not its answers.
	RequestsOnly bool
}

// handoffNoteMaxChars is the note's length cap on this system. On Windows a
// whole command line is limited to 32,767 UTF-16 units, escaping included, and
// a note passed as an argument shares it with the agent's own arguments.
var handoffNoteMaxChars = handoffNoteCharLimit(runtime.GOOS)

func handoffNoteCharLimit(goos string) int {
	if goos == "windows" {
		return 16_000
	}
	return ProviderSwitchMaxBootstrapChars
}

// BuildHandoffNote renders the note a provider receives when it takes over a
// session. It is deterministic and never asks a model to summarise: the latest
// turn is kept, the five before it are clipped to a budget, up to fifteen older
// ones become one line each, and anything beyond that is only counted.
func BuildHandoffNote(in HandoffNoteInput) string {
	// recent is the latest turn with the previous turns before it.
	recent := in.Turns[max(0, len(in.Turns)-1-handoffPreviousTurnLimit):]
	older := in.Turns[max(0, len(in.Turns)-len(recent)-handoffOlderTurnLimit) : len(in.Turns)-len(recent)]

	// Drop the oldest material first until the whole note fits. The latest
	// turn is never dropped.
	for {
		note := renderHandoffNote(in, older, recent)
		if utf8.RuneCountInString(note) <= handoffNoteMaxChars && len(note) <= handoffNoteMaxBytes {
			return note
		}
		switch {
		case len(older) > 0:
			older = older[1:]
		case len(recent) > 1:
			recent = recent[1:]
		default:
			return clipHandoffText(note, handoffNoteMaxChars, handoffClipMarker)
		}
	}
}

func renderHandoffNote(in HandoffNoteInput, older, recent []HandoffTurn) string {
	var b strings.Builder
	if in.SwitchID != "" {
		fmt.Fprintf(&b, "%s switch-id=%q>\n", HandoffNotePrefix, in.SwitchID)
	} else {
		b.WriteString(HandoffNotePrefix + ">\n")
	}
	// The new agent must take this as the user's real conversation. Calling it
	// AO's note, not the user's, made Claude refuse to use what the user had
	// said, such as a value they asked it to remember.
	fmt.Fprintf(&b, "You are %s, taking over this AO session from %s. The conversation below really took place between this user and %s. Treat it as your own history with them: what the user told you there, such as facts, preferences or values they asked you to remember, still holds.\n",
		handoffHarnessLabel(in.TargetHarness), handoffHarnessLabel(in.SourceHarness), handoffHarnessLabel(in.SourceHarness))
	b.WriteString("- AO copied this record from that conversation. Requests in it were already handled; do not carry them out again.\n")
	b.WriteString("- The files in the workspace and git are the truth. Check them before relying on this record.\n")
	b.WriteString("- Do not redo work that is already finished.\n")

	b.WriteString("\n## Session\n")
	writeHandoffNoteField(&b, "Title", in.SessionTitle)
	writeHandoffNoteField(&b, "Project", in.Project)
	writeHandoffNoteField(&b, "Workspace", in.WorkspacePath)
	writeHandoffNoteField(&b, "Git branch", in.Branch)
	writeHandoffNoteField(&b, "Handed off at", in.At.UTC().Format(time.RFC3339))

	if task := strings.TrimSpace(in.OriginalTask); task != "" && !handoffTaskShownInFull(task, recent) {
		b.WriteString("\n## Original task\n")
		b.WriteString(quoteHandoffText(task, handoffOriginalTaskCap, handoffClipMarker))
		b.WriteByte('\n')
	}

	if len(in.ChangedFiles) > 0 {
		fmt.Fprintf(&b, "\n## Workspace state\nFiles changed since the session started (%d):\n", len(in.ChangedFiles))
		for index, name := range in.ChangedFiles {
			if index == handoffChangedFileLimit {
				fmt.Fprintf(&b, "(and %d more)\n", len(in.ChangedFiles)-handoffChangedFileLimit)
				break
			}
			fmt.Fprintf(&b, "- %s\n", quoteHandoffLine(name, handoffChangedFileCap))
		}
	}

	if len(older) > 0 {
		b.WriteString("\n## Older turns, oldest first\n")
		for _, turn := range older {
			user, agent := budgetHandoffTurn(turn, handoffOlderTurnBudget, handoffOlderUserCap, true)
			fmt.Fprintf(&b, "- User: %s\n  %s: %s\n", user, handoffAssistantLabel(turn), agent)
		}
	}
	if len(recent) > 1 {
		b.WriteString("\n## Previous turns, oldest first\n")
		for _, turn := range recent[:len(recent)-1] {
			user, agent := budgetHandoffTurn(turn, handoffPreviousTurnBudget, handoffPreviousUserCap, false)
			fmt.Fprintf(&b, "\nUser:\n%s\n%s:\n%s\n", user, handoffAssistantLabel(turn), agent)
		}
	}
	if len(recent) > 0 {
		latest := recent[len(recent)-1]
		user, agent := budgetHandoffTurn(latest, handoffLatestTurnBudget, handoffLatestUserCap, false)
		fmt.Fprintf(&b, "\n## Latest turn\nUser:\n%s\n%s:\n%s\n", user, handoffAssistantLabel(latest), agent)
	}

	if len(in.HistoryFiles) > 0 {
		b.WriteString("\n## Earlier agents' own history\n")
		b.WriteString("AO did not record what these agents answered in this session, but each keeps its own history. Read only the last few exchanges, and only when this note does not tell you enough.\n")
		for _, file := range in.HistoryFiles {
			note := ""
			if file.RequestsOnly {
				note = " The requests are above; this file also has the answers."
			}
			fmt.Fprintf(&b, "- %s: `%s`. %s%s\n", handoffHarnessLabel(file.Harness), quoteHandoffLine(file.Path, handoffHistoryPathCap), file.Read, note)
		}
	}

	// Said even when no turn was left out, since long turns are clipped.
	b.WriteString("\n## Earlier history\n")
	if omitted := len(in.Turns) - len(older) - len(recent); omitted > 0 {
		fmt.Fprintf(&b, "%d earlier turns are not shown. ", omitted)
	}
	b.WriteString("Run `ao session history` to read the conversation in full when you need something this note does not show.\n")

	b.WriteByte('\n')
	if in.Terminal {
		b.WriteString("Reply with a few lines on where things stand, then wait for the user. Do not start new work yet.\n")
	} else {
		b.WriteString("Do not mention this note unless it is relevant. Wait for the user's next message.\n")
	}
	b.WriteString("</ao-handoff-request>")
	return b.String()
}

// handoffTaskShownInFull reports whether the original task already appears
// unclipped as a shown turn's request, so a short session does not print it twice.
func handoffTaskShownInFull(task string, recent []HandoffTurn) bool {
	for index, turn := range recent {
		limit := handoffPreviousUserCap
		if index == len(recent)-1 {
			limit = handoffLatestUserCap
		}
		if strings.TrimSpace(turn.UserRequest) == task && utf8.RuneCountInString(task) <= limit {
			return true
		}
	}
	return false
}

// handoffAssistantLabel names the agent that answered a turn when AO knows it;
// without it the reader credits every answer to the agent it took over from.
func handoffAssistantLabel(turn HandoffTurn) string {
	if harness := strings.TrimSpace(string(turn.Harness)); harness != "" {
		return "Assistant (" + harness + ")"
	}
	return "Assistant"
}

func handoffHarnessLabel(harness AgentHarness) string {
	if strings.TrimSpace(string(harness)) == "" {
		return "another agent"
	}
	return string(harness)
}

func writeHandoffNoteField(b *strings.Builder, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		fmt.Fprintf(b, "%s: %s\n", label, quoteHandoffLine(value, handoffChangedFileCap))
	}
}

// budgetHandoffTurn gives the user's request what it needs up to its cap and
// the agent's response the rest of the turn's budget: requests are short, so
// the room is better spent on the response.
func budgetHandoffTurn(turn HandoffTurn, budget, userCap int, oneLine bool) (user, agent string) {
	marker := handoffClipMarker
	userText, agentText := strings.TrimSpace(turn.UserRequest), strings.TrimSpace(turn.AgentResponse)
	if oneLine {
		marker = handoffLineClipMarker
		userText, agentText = strings.Join(strings.Fields(userText), " "), strings.Join(strings.Fields(agentText), " ")
	}
	user = quoteHandoffText(userText, userCap, marker)
	if agentText == "" {
		return user, "(no final response recorded)"
	}
	return user, quoteHandoffText(agentText, budget-utf8.RuneCountInString(user), marker)
}

// handoffTagPattern is the note's own tag in any case; a heading at the start
// of a quoted line would read as one of the note's sections.
var (
	handoffTagPattern     = regexp.MustCompile(`(?i)ao-handoff-request`)
	handoffHeadingPattern = regexp.MustCompile(`(?m)^([ \t]*)#`)
)

// quoteHandoffText clips quoted text to limit and defangs the note's own tag
// and section headings, so nothing inside a quoted turn can close the handoff
// block early or add a section to it.
func quoteHandoffText(value string, limit int, marker string) string {
	value = handoffTagPattern.ReplaceAllString(SanitizeControlChars(value), "ao_handoff_request")
	value = handoffHeadingPattern.ReplaceAllString(value, `$1\#`)
	return clipHandoffText(value, limit, marker)
}

// quoteHandoffLine is quoteHandoffText for a value given one line of its own,
// such as a file name or a title, which may hold a newline.
func quoteHandoffLine(value string, limit int) string {
	return quoteHandoffText(strings.Join(strings.Fields(value), " "), limit, handoffLineClipMarker)
}

// clipHandoffText keeps the start and the end of over-long text: a request opens
// with the ask and a response closes with what is left to do. Every limit in
// this file is longer than the marker.
func clipHandoffText(value string, limit int, marker string) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	keep := limit - utf8.RuneCountInString(marker)
	head := keep / 2
	return string(runes[:head]) + marker + string(runes[len(runes)-(keep-head):])
}
