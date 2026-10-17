package chat

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// transcriptExcerptPageRows is one read of the conversation. A turn that ran
	// many tools fills a page quickly, so the excerpt reads older pages until it
	// has enough text or reaches transcriptExcerptMaxRows.
	transcriptExcerptPageRows = 500
	transcriptExcerptMaxRows  = 5000
)

const transcriptExcerptOmitted = "[... earlier conversation omitted ...]"

const (
	// excerptActionLineBytes caps one action line, so a long command or path
	// cannot crowd out the conversation around it.
	excerptActionLineBytes = 200
	// excerptActionsPerRun caps the actions kept between two messages; a turn
	// that ran hundreds of tools keeps its newest ones.
	excerptActionsPerRun = 40
	// excerptFailedOutputBytes is how much of a failed command's output is kept:
	// its tail, where the error usually is.
	excerptFailedOutputBytes = 300
)

// TranscriptMarker is a labelled point in the conversation's history, such as
// an agent switch, placed among the messages by time.
type TranscriptMarker struct {
	At   time.Time
	Text string
}

// TranscriptExcerpt is the newest part of a session's conversation as plain
// text, oldest first, within maxBytes. An agent switch hands it to the next
// agent, so it reads AO's own record: every agent has one, whether or not its
// provider keeps a transcript AO can open. Messages come with the agent's
// actions between them (commands, file changes, tool calls), one line each, and
// markers show where earlier agent switches happened. truncated reports that
// older entries, or the start of the oldest one shown, were left out.
func (s *Service) TranscriptExcerpt(ctx context.Context, id domain.SessionID, maxBytes int, markers []TranscriptMarker) (excerpt string, truncated bool, err error) {
	var messages []domain.ConversationMessage
	var activities []domain.ConversationActivity
	before := int64(0)
	rows := 0
	var more bool
	for {
		snapshot, err := s.SnapshotPage(ctx, id, before, transcriptExcerptPageRows)
		if err != nil {
			return "", false, err
		}
		messages = append(snapshot.Messages, messages...)
		activities = append(snapshot.Activities, activities...)
		rows += len(snapshot.Messages) + len(snapshot.Activities)
		more = snapshot.HasMoreBefore
		oldest := oldestSequence(snapshot.Messages, snapshot.Activities)
		if !more || oldest <= 0 || rows >= transcriptExcerptMaxRows || transcriptTextBytes(messages) >= maxBytes {
			break
		}
		before = oldest
	}
	excerpt, truncated = transcriptExcerpt(transcriptBlocks(messages, activities, markers), maxBytes)
	return excerpt, truncated || more, nil
}

func oldestSequence(messages []domain.ConversationMessage, activities []domain.ConversationActivity) int64 {
	oldest := int64(0)
	for _, message := range messages {
		if oldest == 0 || message.Sequence < oldest {
			oldest = message.Sequence
		}
	}
	for _, activity := range activities {
		if oldest == 0 || activity.Sequence < oldest {
			oldest = activity.Sequence
		}
	}
	return oldest
}

func transcriptTextBytes(messages []domain.ConversationMessage) int {
	n := 0
	for _, message := range messages {
		n += len(message.Text)
	}
	return n
}

// transcriptBlocks renders messages, the actions between them, and markers in
// timeline order: one block per message, one "Actions" block per run of
// actions, and one line per marker.
func transcriptBlocks(messages []domain.ConversationMessage, activities []domain.ConversationActivity, markers []TranscriptMarker) []string {
	type entry struct {
		sequence int64
		// order breaks ties: a marker sorts before the message it precedes.
		order   int
		message *domain.ConversationMessage
		action  string
		marker  string
	}
	entries := make([]entry, 0, len(messages)+len(activities)+len(markers))
	for i := range messages {
		entries = append(entries, entry{sequence: messages[i].Sequence, order: 1, message: &messages[i]})
	}
	for _, activity := range activities {
		if line := transcriptAction(activity); line != "" {
			entries = append(entries, entry{sequence: activity.Sequence, order: 1, action: line})
		}
	}
	for _, marker := range markers {
		// A marker goes before the first message written at or after it. One
		// newer than every message shown (or older than every one) is outside
		// this excerpt's window and is left out.
		for i := range messages {
			if !messages[i].CreatedAt.Before(marker.At) {
				if i > 0 {
					entries = append(entries, entry{sequence: messages[i].Sequence, order: 0, marker: marker.Text})
				}
				break
			}
		}
	}
	sort.SliceStable(entries, func(i, j int) bool {
		if entries[i].sequence != entries[j].sequence {
			return entries[i].sequence < entries[j].sequence
		}
		return entries[i].order < entries[j].order
	})

	var blocks []string
	var actions []string
	flush := func() {
		if len(actions) == 0 {
			return
		}
		header := "Actions:"
		if dropped := len(actions) - excerptActionsPerRun; dropped > 0 {
			header = fmt.Sprintf("Actions (%d earlier omitted):", dropped)
			actions = actions[dropped:]
		}
		blocks = append(blocks, header+"\n- "+strings.Join(actions, "\n- "))
		actions = nil
	}
	for _, entry := range entries {
		switch {
		case entry.marker != "":
			flush()
			blocks = append(blocks, "--- "+entry.marker+" ---")
		case entry.message == nil:
			actions = append(actions, entry.action)
		default:
			text := strings.TrimSpace(entry.message.Text)
			if text == "" {
				continue
			}
			flush()
			blocks = append(blocks, transcriptSpeaker(*entry.message)+":\n"+indentBody(text))
		}
	}
	flush()
	return blocks
}

// indentBody indents every line of a message, so only real speaker labels
// start a line: quoted text such as "User: ..." inside a message cannot pass
// for a turn of its own.
func indentBody(text string) string {
	return "  " + strings.ReplaceAll(text, "\n", "\n  ")
}

// transcriptAction is one line for an action worth handing over, or "" for
// noise the next agent does not need (reasoning, usage, system notices).
func transcriptAction(activity domain.ConversationActivity) string {
	summary := strings.Join(strings.Fields(activity.Summary), " ")
	if summary == "" {
		return ""
	}
	var line string
	switch activity.Kind {
	case domain.ActivityKindCommand:
		line = "ran " + redactSecrets(summary)
	case domain.ActivityKindFileChange:
		line = "changed " + summary
	case domain.ActivityKindMCPTool:
		line = "tool " + redactSecrets(summary)
	case domain.ActivityKindPlan:
		line = "plan " + summary
	case domain.ActivityKindError:
		line = "error " + redactSecrets(summary)
	case domain.ActivityKindApproval:
		line = "asked approval for " + redactSecrets(summary) + " (" + string(activity.Status) + ")"
	case domain.ActivityKindUserInput:
		line = "asked the user " + summary + " (" + string(activity.Status) + ")"
	default:
		return ""
	}
	line = clipText(line, excerptActionLineBytes)
	if activity.Kind == domain.ActivityKindApproval || activity.Kind == domain.ActivityKindUserInput {
		return line
	}
	if status := activity.Status; status == domain.ActivityStatusFailed || status == domain.ActivityStatusCancelled {
		line += " (" + string(status) + ")"
		if activity.Kind == domain.ActivityKindCommand {
			if output := strings.TrimSpace(activity.CommandOutput); output != "" {
				tail := output[len(output)-min(len(output), excerptFailedOutputBytes):]
				line += "\n  output: " + redactSecrets(strings.Join(strings.Fields(strings.ToValidUTF8(tail, "")), " "))
			}
		}
	}
	return line
}

// secretPatterns match credentials that commonly appear in command lines and
// their output. The handoff goes to another vendor's agent, so these are
// masked; anything else in the transcript is handed over as written.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|basic|token)\s+)[^\s"']+`),
	regexp.MustCompile(`(?i)(\b[\w.-]*(?:api[_-]?key|token|secret|password|passwd|pwd|credentials?)\s*[=:]\s*["']?)[^\s"'&]+`),
	regexp.MustCompile(`\b(sk|pk|rk)-[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`\bgh[pousr]_[A-Za-z0-9]{20,}`),
	regexp.MustCompile(`\bxox[abprs]-[A-Za-z0-9-]{10,}`),
	regexp.MustCompile(`\bAKIA[0-9A-Z]{16}\b`),
	regexp.MustCompile(`(?i)(://[^/\s:@]+:)[^@\s/]+@`),
}

func redactSecrets(s string) string {
	for i, pattern := range secretPatterns {
		switch i {
		case 0, 1:
			s = pattern.ReplaceAllString(s, "${1}[redacted]")
		case len(secretPatterns) - 1:
			s = pattern.ReplaceAllString(s, "${1}[redacted]@")
		default:
			s = pattern.ReplaceAllString(s, "[redacted]")
		}
	}
	return s
}

// clipText cuts s to at most n bytes on a rune boundary, marking the cut.
func clipText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return strings.ToValidUTF8(s[:n], "") + "..."
}

func transcriptExcerpt(blocks []string, maxBytes int) (string, bool) {
	var kept []string
	size := 0
	truncated := false
	for i := len(blocks) - 1; i >= 0; i-- {
		block := blocks[i]
		if size+len(block)+2 > maxBytes {
			truncated = true
			// Keep the end of the newest block even when it alone is too long:
			// its conclusion is what the next agent most needs. The speaker
			// label at its start goes back in front of what is kept.
			if len(kept) == 0 && maxBytes > 0 {
				label := ""
				if head, _, ok := strings.Cut(block, "\n"); ok && strings.HasSuffix(head, ":") {
					label = head + "\n"
				}
				keep := max(maxBytes-len(label)-len(transcriptExcerptOmitted)-1, 0)
				kept = append(kept, transcriptExcerptOmitted+"\n"+label+strings.ToValidUTF8(block[len(block)-min(len(block), keep):], ""))
			}
			break
		}
		kept = append(kept, block)
		size += len(block) + 2
	}
	for left, right := 0, len(kept)-1; left < right; left, right = left+1, right-1 {
		kept[left], kept[right] = kept[right], kept[left]
	}
	if truncated && len(kept) > 0 && !strings.HasPrefix(kept[0], transcriptExcerptOmitted) {
		kept = append([]string{transcriptExcerptOmitted}, kept...)
	}
	return strings.Join(kept, "\n\n"), truncated
}

func transcriptSpeaker(message domain.ConversationMessage) string {
	if message.Role == domain.MessageRoleAssistant {
		return "Assistant"
	}
	if message.Origin == domain.MessageOriginHuman || message.Origin == "" {
		return "User"
	}
	if name := strings.TrimSpace(message.SenderDisplayName); name != "" {
		return "AO (from " + strings.Join(strings.Fields(name), " ") + ")"
	}
	return "AO"
}
