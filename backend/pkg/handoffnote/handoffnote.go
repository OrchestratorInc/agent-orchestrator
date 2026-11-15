// Package handoffnote renders the note a coding agent receives when it takes
// over a session from another agent: the desktop daemon's provider switch and
// AO Cloud's agent switch share it, so an agent gets the same record of the
// conversation wherever the switch happens. The desktop daemon also gives it to
// the agent of a fork that can't copy its own conversation (Input.Fork). It is
// deterministic and never asks a model to summarise.
package handoffnote

import (
	"fmt"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	// Prefix opens every handoff note.
	Prefix = "<ao-handoff-request"

	// DefaultMaxChars is a note's length cap when Input.MaxChars is unset.
	DefaultMaxChars = 32_000
	// MaxBytes caps a note's size: one delivered as a launch argument must
	// also fit the operating system's per-argument limit, which is counted in
	// bytes.
	MaxBytes = 96_000

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
	handoffArtifactLimit    = 50
	handoffHistoryPathCap   = 400
	handoffTerminalCap      = 8_000
	handoffInheritedCap     = 24_000
	// handoffInheritedMin is the shortest inherited section worth keeping.
	handoffInheritedMin = 500

	handoffClipMarker     = "\n…[clipped]…\n"
	handoffLineClipMarker = " …[clipped]… "
)

// Turn is one turn of a session, whichever UI it happened in.
type Turn struct {
	UserRequest   string
	AgentResponse string
	At            time.Time
	// Harness is the agent that answered it, when known.
	Harness string
}

// Artifact is one file in the session's artifact directory.
type Artifact struct {
	Path      string
	Size      int64
	UpdatedAt time.Time
}

// HistoryFile is one agent's own record of this session and how to read it.
type HistoryFile struct {
	Harness string
	Path    string
	// Read says how to find the messages in the file.
	Read string
	// RequestsOnly marks an agent whose requests AO recorded but not its answers.
	RequestsOnly bool
}

// Input is everything a handoff note is rendered from. Turns holds every turn
// AO knows about, oldest first; the builder decides what fits.
type Input struct {
	SwitchID      string
	SourceHarness string
	TargetHarness string
	SessionTitle  string
	Project       string
	WorkspacePath string
	Branch        string
	At            time.Time
	OriginalTask  string
	ChangedFiles  []string
	// ArtifactDir is the session's artifact directory and Artifacts the files
	// in it, in the order the note lists them.
	ArtifactDir string
	Artifacts   []Artifact
	Turns       []Turn
	Terminal    bool
	// HistoryFiles point at the agent's own record for an agent whose answers
	// AO did not record, because it reports no turns through hooks.
	HistoryFiles []HistoryFile
	// Fork, when set, makes this a fork's note: the agent starts a new session
	// copied from that one instead of taking it over.
	Fork *Fork
	// TerminalOutput is the end of the session's terminal screen, for agents
	// whose turns AO may not have recorded.
	TerminalOutput string
	// Inherited is the note the session itself started with, when it was a
	// fork given its conversation as text: the conversation from before it,
	// which its own turns don't hold.
	Inherited string
	// FullHistory tells the agent where it can read the whole conversation,
	// beyond what the note shows ("Run `ao session history` ..."); empty says
	// nothing.
	FullHistory string
	// MaxChars caps the note's length in characters; DefaultMaxChars when 0.
	MaxChars int
}

// Fork names the session a text-handoff fork was copied from.
type Fork struct {
	SourceSessionID string
	SourceTitle     string
}

func (in Input) maxChars() int {
	if in.MaxChars > 0 {
		return in.MaxChars
	}
	return DefaultMaxChars
}

// Build renders the note an agent receives when it takes over a session, or
// starts a fork of one. It is deterministic and never asks a model to
// summarise: the latest turn is kept, the five before it are clipped to a
// budget, up to fifteen older ones become one line each, and anything beyond
// that is only counted. A fork's inherited conversation, being the oldest, is
// shortened first.
func Build(in Input) string {
	// recent is the latest turn with the previous turns before it.
	recent := in.Turns[max(0, len(in.Turns)-1-handoffPreviousTurnLimit):]
	older := in.Turns[max(0, len(in.Turns)-len(recent)-handoffOlderTurnLimit) : len(in.Turns)-len(recent)]

	artifacts := min(len(in.Artifacts), handoffArtifactLimit)
	inherited := strings.TrimSpace(in.Inherited)
	inheritedCap := handoffInheritedCap
	if inherited == "" {
		inheritedCap = 0
	}

	// Drop material until the whole note fits: the conversation inherited
	// from before the session, being the oldest, then older turns, the last
	// listed artifacts, and previous turns. The latest turn is never dropped.
	for {
		note := render(in, older, recent, artifacts, inheritedCap)
		if fits(note, in.maxChars()) {
			return note
		}
		switch {
		case inheritedCap > 0:
			// Shorten it by what the note is over; a rune is at most four bytes.
			over := max(utf8.RuneCountInString(note)-in.maxChars(), (len(note)-MaxBytes+3)/4, 1)
			inheritedCap = utf8.RuneCountInString(quoteHandoffText(inherited, inheritedCap, handoffClipMarker)) - over
			if inheritedCap < handoffInheritedMin {
				inheritedCap = 0
			}
		case len(older) > 0:
			older = older[1:]
		case artifacts > 0:
			artifacts--
		case len(recent) > 1:
			recent = recent[1:]
		default:
			return clipNote(note, in.maxChars())
		}
	}
}

func fits(note string, maxChars int) bool {
	return utf8.RuneCountInString(note) <= maxChars && len(note) <= MaxBytes
}

// clipNote clips a note to both the character and the byte cap.
func clipNote(note string, maxChars int) string {
	limit := maxChars
	for {
		clipped := clipHandoffText(note, limit, handoffClipMarker)
		if len(clipped) <= MaxBytes {
			return clipped
		}
		// A rune is at most four bytes.
		limit -= max(1, (len(clipped)-MaxBytes)/4)
	}
}

// render renders the note with the given turns, the first artifacts
// entries of in.Artifacts, and in.Inherited clipped to inheritedCap, or left
// out at zero.
func render(in Input, older, recent []Turn, artifacts, inheritedCap int) string {
	var b strings.Builder
	if in.SwitchID != "" {
		fmt.Fprintf(&b, "%s switch-id=%q>\n", Prefix, in.SwitchID)
	} else {
		b.WriteString(Prefix + ">\n")
	}
	// The new agent must take this as the user's real conversation. Calling it
	// AO's note, not the user's, made Claude refuse to use what the user had
	// said, such as a value they asked it to remember.
	if fork := in.Fork; fork != nil {
		title := ""
		if t := strings.TrimSpace(fork.SourceTitle); t != "" {
			title = fmt.Sprintf(" (%q)", quoteHandoffLine(t, handoffChangedFileCap))
		}
		fmt.Fprintf(&b, "You are %s in a new Orchestrator session forked from Orchestrator session %s%s. %s can't copy that session's conversation itself, so the conversation is copied as text below. The conversation really took place between this user and %s. Treat it as your own history with them: what the user told you there, such as facts, preferences or values they asked you to remember, still holds.\n",
			handoffHarnessLabel(in.TargetHarness), fork.SourceSessionID, title, handoffHarnessLabel(in.TargetHarness), handoffHarnessLabel(in.SourceHarness))
	} else {
		fmt.Fprintf(&b, "You are %s, taking over this Orchestrator-managed session from %s. The conversation below really took place between this user and %s. Treat it as your own history with them: what the user told you there, such as facts, preferences or values they asked you to remember, still holds.\n",
			handoffHarnessLabel(in.TargetHarness), handoffHarnessLabel(in.SourceHarness), handoffHarnessLabel(in.SourceHarness))
	}
	b.WriteString("- Orchestrator copied this record from that conversation. Requests in it were already handled; do not carry them out again.\n")
	b.WriteString("- The files in the workspace and git are the truth. Check them before relying on this record.\n")
	b.WriteString("- Do not redo work that is already finished.\n")

	b.WriteString("\n## Session\n")
	writeHandoffNoteField(&b, "Title", in.SessionTitle)
	writeHandoffNoteField(&b, "Project", in.Project)
	writeHandoffNoteField(&b, "Workspace", in.WorkspacePath)
	writeHandoffNoteField(&b, "Git branch", in.Branch)
	if in.Fork != nil {
		writeHandoffNoteField(&b, "Forked at", in.At.UTC().Format(time.RFC3339))
	} else {
		writeHandoffNoteField(&b, "Handed off at", in.At.UTC().Format(time.RFC3339))
	}

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

	if len(in.Artifacts) > 0 {
		fmt.Fprintf(&b, "\n## Session artifacts\nFiles already in the session artifact directory `%s` (%d). Read the one a request is about before writing it again, and update it instead of starting over:\n",
			quoteHandoffLine(in.ArtifactDir, handoffHistoryPathCap), len(in.Artifacts))
		for _, file := range in.Artifacts[:artifacts] {
			fmt.Fprintf(&b, "- %s (%d bytes, updated %s)\n", quoteHandoffLine(file.Path, handoffChangedFileCap), file.Size, file.UpdatedAt.UTC().Format(time.RFC3339))
		}
		if hidden := len(in.Artifacts) - artifacts; hidden > 0 {
			fmt.Fprintf(&b, "(%d more not listed; list the directory to see them)\n", hidden)
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

	if inherited := strings.TrimSpace(in.Inherited); inherited != "" && inheritedCap > 0 {
		b.WriteString("\n## Conversation before that session\nThat session was itself started from an earlier conversation, given to it as text. It is part of the same history:\n")
		b.WriteString(quoteHandoffText(inherited, inheritedCap, handoffClipMarker))
		b.WriteByte('\n')
	}

	if output := strings.TrimSpace(in.TerminalOutput); output != "" {
		b.WriteString("\n## Recent terminal output\nThe end of the session's terminal screen, as text. It may repeat the turns above or show what they missed:\n")
		b.WriteString(quoteHandoffText(tailHandoffText(output, handoffTerminalCap), handoffTerminalCap+utf8.RuneCountInString(handoffClipMarker), handoffClipMarker))
		b.WriteByte('\n')
	}

	if len(in.HistoryFiles) > 0 {
		b.WriteString("\n## Earlier agents' own history\n")
		b.WriteString("Orchestrator did not record what these agents answered in this session, but each keeps its own history. Read only the last few exchanges, and only when this note does not tell you enough.\n")
		for _, file := range in.HistoryFiles {
			note := ""
			if file.RequestsOnly {
				note = " The requests are above; this file also has the answers."
			}
			fmt.Fprintf(&b, "- %s: `%s`. %s%s\n", handoffHarnessLabel(file.Harness), quoteHandoffLine(file.Path, handoffHistoryPathCap), file.Read, note)
		}
	}

	// Said even when no turn was left out, since long turns are clipped.
	omitted := len(in.Turns) - len(older) - len(recent)
	if omitted > 0 || in.FullHistory != "" {
		b.WriteString("\n## Earlier history\n")
		var said []string
		if omitted > 0 {
			said = append(said, fmt.Sprintf("%d earlier turns are not shown.", omitted))
		}
		if in.FullHistory != "" {
			said = append(said, in.FullHistory)
		}
		b.WriteString(strings.Join(said, " ") + "\n")
	}

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
func handoffTaskShownInFull(task string, recent []Turn) bool {
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
func handoffAssistantLabel(turn Turn) string {
	if harness := strings.TrimSpace(turn.Harness); harness != "" {
		return "Assistant (" + harness + ")"
	}
	return "Assistant"
}

func handoffHarnessLabel(harness string) string {
	if strings.TrimSpace(harness) == "" {
		return "another agent"
	}
	return harness
}

func writeHandoffNoteField(b *strings.Builder, label, value string) {
	if value = strings.TrimSpace(value); value != "" {
		fmt.Fprintf(b, "%s: %s\n", label, quoteHandoffLine(value, handoffChangedFileCap))
	}
}

// budgetHandoffTurn gives the user's request what it needs up to its cap and
// the agent's response the rest of the turn's budget: requests are short, so
// the room is better spent on the response.
func budgetHandoffTurn(turn Turn, budget, userCap int, oneLine bool) (user, agent string) {
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

// tailHandoffText keeps the end of over-long text, marked: the newest
// terminal output is what the next agent needs.
func tailHandoffText(value string, limit int) string {
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return "…[earlier output omitted]…\n" + string(runes[len(runes)-limit:])
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
	value = handoffTagPattern.ReplaceAllString(sanitizeControlChars(value), "ao_handoff_request")
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

// sanitizeControlChars drops control characters other than newlines, carriage
// returns and tabs.
func sanitizeControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\r' && r != '\t' {
			return -1
		}
		return r
	}, s)
}
