package chat

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// SessionTurns returns every turn AO holds for a session, oldest first: the
// completed Chat exchanges merged by time with the stored Terminal UI turns.
// A turn is written to one of the two stores only, so merging never repeats it.
// A session that has never been in Chat simply has no conversation.
func (s *Service) SessionTurns(ctx context.Context, id domain.SessionID) ([]domain.HandoffTurn, error) {
	var turns []domain.HandoffTurn
	conversation, err := s.store.ConversationForSession(ctx, id)
	switch {
	case errors.Is(err, domain.ErrNoConversation):
	case err != nil:
		return nil, err
	case s.reader != nil:
		rows, readErr := s.reader.LoadConversationSnapshot(ctx, conversation.ID)
		if readErr != nil {
			return nil, fmt.Errorf("load conversation %s turns: %w", conversation.ID, readErr)
		}
		turns = chatHandoffTurns(rows.Turns, rows.Messages, rows.Activities)
	}
	stored, err := s.store.ListTerminalTurns(ctx, id)
	if err != nil {
		return nil, err
	}
	turns = append(turns, stored...)
	sort.SliceStable(turns, func(i, j int) bool { return turns[i].At.Before(turns[j].At) })
	return turns, nil
}

// chatHandoffTurns reduces a conversation to one request and one final response
// per completed turn. A request is what a person typed or what was sent to the
// session through AO; the daemon's own messages are not requests.
//
// Each turn is credited to the provider that answered it: the one the newest
// switch marker before it brought in. Turns before the first marker were the
// first source's. Sequences are the conversation's, so an edit's new branch,
// whose turns come after the marker of the agent it runs on, is credited too.
func chatHandoffTurns(turns []domain.ConversationTurn, messages []domain.ConversationMessage, activities []domain.ConversationActivity) []domain.HandoffTurn {
	type marker struct {
		sequence int64
		target   domain.AgentHarness
	}
	var markers []marker
	var firstSource domain.AgentHarness
	for _, activity := range activities {
		var detail providerSwitchActivityDetail
		if activity.Kind != domain.ActivityKindSystem || json.Unmarshal(activity.Detail, &detail) != nil || detail.Event != "provider_switch" {
			continue
		}
		if len(markers) == 0 {
			firstSource = detail.SourceHarness
		}
		markers = append(markers, marker{sequence: activity.Sequence, target: detail.TargetHarness})
	}
	sort.Slice(markers, func(i, j int) bool { return markers[i].sequence < markers[j].sequence })
	answeredBy := func(sequence int64) domain.AgentHarness {
		harness := firstSource
		for _, m := range markers {
			if m.sequence < sequence {
				harness = m.target
			}
		}
		return harness
	}
	type exchange struct {
		user, assistant *domain.ConversationMessage
	}
	exchanges := make(map[string]*exchange, len(turns))
	for _, turn := range turns {
		if turn.State == domain.TurnStateCompleted && turn.RolledBackAt == nil {
			exchanges[turn.ID] = &exchange{}
		}
	}
	for index := range messages {
		message := &messages[index]
		pair := exchanges[message.TurnID]
		if pair == nil || message.Streaming {
			continue
		}
		switch message.Role {
		case domain.MessageRoleUser:
			asked := message.Origin == domain.MessageOriginHuman || message.Origin == domain.MessageOriginAutomation
			if asked && (pair.user == nil || message.Sequence < pair.user.Sequence) {
				pair.user = message
			}
		case domain.MessageRoleAssistant:
			if pair.assistant == nil || message.Sequence > pair.assistant.Sequence {
				pair.assistant = message
			}
		}
	}
	complete := make([]*exchange, 0, len(exchanges))
	for _, pair := range exchanges {
		if pair.user != nil && pair.assistant != nil && strings.TrimSpace(pair.user.Text) != "" {
			complete = append(complete, pair)
		}
	}
	sort.Slice(complete, func(i, j int) bool { return complete[i].user.Sequence < complete[j].user.Sequence })
	out := make([]domain.HandoffTurn, 0, len(complete))
	for _, pair := range complete {
		out = append(out, domain.HandoffTurn{UserRequest: pair.user.Text, AgentResponse: pair.assistant.Text, At: pair.user.CreatedAt, Harness: answeredBy(pair.user.Sequence)})
	}
	return out
}

// HandoffNote builds the note a provider receives when it takes over record's
// session. It reads AO's own stores and the workspace only: no provider is
// asked to summarise, and nothing is written.
func (s *Service) HandoffNote(ctx context.Context, record domain.SessionRecord, target domain.AgentHarness, switchID string) (string, error) {
	input, err := s.handoffNoteInput(ctx, record, target, switchID)
	if err != nil {
		return "", err
	}
	input.ChangedFiles = handoffChangedFiles(ctx, record.Metadata.WorkspacePath, record.Metadata.DiffBaseSHA)
	return domain.BuildHandoffNote(input), nil
}

// handoffNoteInput is the note's input without the changed files, which need
// git and can run while the outgoing provider stops.
func (s *Service) handoffNoteInput(ctx context.Context, record domain.SessionRecord, target domain.AgentHarness, switchID string) (domain.HandoffNoteInput, error) {
	turns, err := s.SessionTurns(ctx, record.ID)
	if err != nil {
		return domain.HandoffNoteInput{}, err
	}
	return domain.HandoffNoteInput{
		SwitchID:      switchID,
		SourceHarness: record.Harness,
		TargetHarness: target,
		SessionTitle:  record.DisplayName,
		Project:       string(record.ProjectID),
		WorkspacePath: record.Metadata.WorkspacePath,
		Branch:        record.Metadata.Branch,
		At:            s.now(),
		OriginalTask:  record.Metadata.Prompt,
		Turns:         turns,
		Terminal:      domain.NormalizeSessionMode(record.Mode) == domain.SessionModeTUI,
		HistoryFiles:  s.handoffHistoryFiles(record, turns),
	}, nil
}

// SessionHistory returns one page of a session's turns, newest first, with the
// total number AO holds. It backs `ao session history`, which lets the agent
// that took over read what the handoff note left out.
func (s *Service) SessionHistory(ctx context.Context, id domain.SessionID, offset, limit int) ([]domain.HandoffTurn, int, error) {
	if _, found, err := s.sessions.GetSession(ctx, id); err != nil {
		return nil, 0, err
	} else if !found {
		return nil, 0, ports.ErrSessionNotFound
	}
	turns, err := s.SessionTurns(ctx, id)
	if err != nil {
		return nil, 0, err
	}
	total := len(turns)
	page := make([]domain.HandoffTurn, 0, limit)
	for index := total - 1 - offset; index >= 0 && len(page) < limit; index-- {
		page = append(page, turns[index])
	}
	return page, total, nil
}

// handoffChangedFiles lists what the session changed in its workspace: tracked
// files that differ from the diff base, then untracked ones. It is the note's
// view of what is on disk, since tool calls are not carried. A workspace git
// cannot read yields no list; that must never block a switch.
func handoffChangedFiles(ctx context.Context, workspace, base string) []string {
	if strings.TrimSpace(workspace) == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	seen := make(map[string]bool)
	var files []string
	for _, args := range [][]string{
		{"diff", "--name-only", "-z", cmp.Or(strings.TrimSpace(base), "HEAD")},
		{"ls-files", "-z", "-o", "--exclude-standard"},
	} {
		globalArgs := []string{"--no-pager", "--no-optional-locks", "-c", "core.hooksPath=" + os.DevNull, "-c", "core.fsmonitor=false", "-C", workspace}
		cmd := aoprocess.CommandContext(ctx, "git", append(globalArgs, args...)...)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0", "GCM_INTERACTIVE=Never")
		// A child git leaves holding the pipe open must not stretch the timeout.
		cmd.WaitDelay = time.Second
		out, err := cmd.Output()
		if err != nil {
			continue
		}
		for _, name := range bytes.Split(out, []byte{0}) {
			if file := string(name); file != "" && !seen[file] {
				seen[file] = true
				files = append(files, file)
			}
		}
	}
	return files
}
