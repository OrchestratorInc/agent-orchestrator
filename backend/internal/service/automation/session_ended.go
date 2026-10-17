package automation

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const (
	// maxRunSessionNameRunes matches the session display-name cap.
	maxRunSessionNameRunes = 100
	// maxEndedTaskRunes bounds the ended session's task quoted in the prompt.
	maxEndedTaskRunes = 300
)

// triggerStore is the persistence the session-ended trigger needs.
type triggerStore interface {
	GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error)
	GetAutomationRun(context.Context, domain.AutomationRunID) (domain.AutomationRun, bool, error)
	ListSessionEndedAutomations(context.Context, domain.ProjectID) ([]domain.Automation, error)
	CreateTriggeredAutomationRun(context.Context, domain.AutomationRun) (bool, error)
}

// SessionEnded queues one run of every enabled session-ended automation in the
// ended session's project and reports whether it queued any. Only workers
// that finished launching count. A session started by a session-ended run
// never starts another one, so reviews cannot loop.
func (s *Service) SessionEnded(ctx context.Context, id domain.SessionID) (bool, error) {
	store, ok := s.store.(triggerStore)
	if !ok {
		return false, fmt.Errorf("automation trigger storage is unavailable")
	}
	session, found, err := store.GetSession(ctx, id)
	if err != nil || !found {
		return false, err
	}
	if session.Kind != domain.KindWorker || session.ProjectID == "" || session.IsTaskPreparation ||
		session.ProvisionState.WithDefault() != domain.SessionProvisionReady {
		return false, nil
	}
	if session.AutomationRunID != nil {
		origin, found, err := store.GetAutomationRun(ctx, *session.AutomationRunID)
		if err != nil {
			return false, err
		}
		if found && origin.TriggerSessionID != nil {
			return false, nil
		}
	}
	definitions, err := store.ListSessionEndedAutomations(ctx, session.ProjectID)
	if err != nil {
		return false, err
	}
	now := s.clock().UTC()
	queued := false
	var problems []error
	for _, definition := range definitions {
		created, err := store.CreateTriggeredAutomationRun(ctx, domain.AutomationRun{
			ID: domain.AutomationRunID("automation-run-" + s.newID()), AutomationID: definition.ID,
			ScheduledFor: now, TriggerSessionID: &id, Status: domain.AutomationRunPending,
			CreatedAt: now, UpdatedAt: now,
		})
		if err != nil {
			problems = append(problems, fmt.Errorf("automation %s: %w", definition.ID, err))
		}
		queued = queued || created
	}
	return queued, errors.Join(problems...)
}

// transcriptLocator is the optional session-service lookup for an ended
// session's native transcript.
type transcriptLocator interface {
	TranscriptPath(context.Context, domain.SessionID) string
}

// sessionEndedLaunch appends the ended session's facts to the run's prompt and
// names the new session after the one that ended. Lookups are best effort: a
// run still launches with the session ID when the row or its PRs are gone.
func sessionEndedLaunch(ctx context.Context, store schedulerStore, transcripts transcriptLocator, definition domain.Automation, id domain.SessionID) (prompt, displayName string) {
	title := string(id)
	var b strings.Builder
	b.WriteString(definition.Prompt)
	b.WriteString("\n\nThis run started because an AO session ended:\n")
	fmt.Fprintf(&b, "- Session: %s\n", id)
	if session, ok, err := store.GetSession(ctx, id); err == nil && ok {
		if name := strings.TrimSpace(session.DisplayName); name != "" {
			title = name
			fmt.Fprintf(&b, "- Title: %s\n", name)
		}
		if branch := session.Metadata.Branch; branch != "" {
			fmt.Fprintf(&b, "- Branch: %s\n", branch)
		}
		if prs, err := store.ListPRsBySession(ctx, id); err == nil {
			for _, pr := range prs {
				state := "open"
				if pr.Merged {
					state = "merged"
				} else if pr.Closed {
					state = "closed"
				}
				fmt.Fprintf(&b, "- Pull request: %s (%s)\n", pr.URL, state)
			}
		}
		if task := oneLine(session.Metadata.Prompt, maxEndedTaskRunes); task != "" {
			fmt.Fprintf(&b, "- Task: %s\n", task)
		}
		transcript := session.Metadata.NativeTranscriptPath
		if transcripts != nil {
			if located := transcripts.TranscriptPath(ctx, id); located != "" {
				transcript = located
			}
		}
		if transcript != "" {
			fmt.Fprintf(&b, "- Transcript: %s\n", transcript)
		}
	}
	fmt.Fprintf(&b, "Run `ao session get %s` for more detail.", id)
	return b.String(), oneLine(definition.DisplayName+": "+title, maxRunSessionNameRunes)
}

// oneLine collapses whitespace and cuts text to maxRunes, marking a cut with
// an ellipsis.
func oneLine(text string, maxRunes int) string {
	text = strings.Join(strings.Fields(text), " ")
	if utf8.RuneCountInString(text) <= maxRunes {
		return text
	}
	return strings.TrimSpace(string([]rune(text)[:maxRunes-1])) + "…"
}
