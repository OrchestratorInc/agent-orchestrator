package claudeacp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatDriverReconnector = (*checkpointDriver)(nil)

type checkpointDriver struct {
	ports.ChatDriver
	plugin claudePlugin
}

// Reconnect forwards to the wrapped driver. Embedding ports.ChatDriver exposes
// only that interface's methods, so without this the startup health check never
// finds a reconnector and marks every live Claude chat as exited.
func (d *checkpointDriver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	reconnector, ok := d.ChatDriver.(ports.ChatDriverReconnector)
	if !ok {
		return nil, ports.ErrChatHostNotRunning
	}
	return reconnector.Reconnect(ctx, cfg)
}

func (d *checkpointDriver) VerifyNativeCheckpoint(ctx context.Context, request ports.NativeCheckpointRequest) (ports.NativeCheckpointBoundary, error) {
	config, ok := d.plugin.(ports.AgentNativeSessionConfigProvider)
	if !ok {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native config resolver unavailable")
	}
	locator, ok := d.plugin.(ports.AgentTranscriptLocator)
	if !ok {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native transcript locator unavailable")
	}
	dir, err := config.NativeSessionConfigDir(ctx, request.Env)
	if err != nil {
		return ports.NativeCheckpointBoundary{}, err
	}
	path, found, err := locator.LocateTranscript(ctx, ports.NativeSessionRef{NativeSessionID: request.ProviderConversationID, ConfigDir: dir})
	if err != nil {
		return ports.NativeCheckpointBoundary{}, err
	}
	if !found {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native transcript not flushed")
	}
	file, err := os.Open(path)
	if err != nil {
		return ports.NativeCheckpointBoundary{}, err
	}
	defer func() { _ = file.Close() }()
	return verifyCheckpointTranscript(ctx, file, request)
}

func checkpointUnsettled(reason string) error {
	return fmt.Errorf("claude native checkpoint: %s: %w", reason,
		&ports.ChatHistoryUnsettledError{Dimensions: []ports.ChatHistoryMismatchDimension{ports.ChatHistoryMismatchUnsettledBoundary}})
}

type checkpointRecord struct {
	UUID                  string `json:"uuid"`
	ParentUUID            string `json:"parentUuid"`
	SessionID             string `json:"sessionId"`
	PromptID              string `json:"promptId"`
	Type                  string `json:"type"`
	Subtype               string `json:"subtype"`
	Sidechain             bool   `json:"isSidechain"`
	Meta                  bool   `json:"isMeta"`
	TurnCompanion         bool   `json:"turnCompanion"`
	PreventedContinuation bool   `json:"preventedContinuation"`
	Attachment            struct {
		Type      string          `json:"type"`
		HookEvent string          `json:"hookEvent"`
		Content   json.RawMessage `json:"content"`
	} `json:"attachment"`
	Message struct {
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
	} `json:"message"`
}

type checkpointTurn struct {
	promptID, userID, user, assistant string
	complete, notice                  bool
}

// claudeTranscriptNotice reports a note Claude Code writes into its transcript
// as a user message: a background task's <task-notification>, which can carry
// text after its closing tag, or the marker for a stopped turn. Chat never
// shows either as a user message.
// ponytail: replay sees only the text, because getSessionMessages drops the
// record's origin, so a human prompt that starts with the same tag is skipped
// too. Reading origin from the transcript in the bridge would remove the guess.
func claudeTranscriptNotice(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<task-notification>") ||
		strings.HasPrefix(text, "[Request interrupted by user") && strings.HasSuffix(text, "]")
}

// Native logs are append-only graphs, not a list of alternating text messages.
// Follow the last main-chain leaf's parent UUIDs; never use a matching sidechain,
// an earlier repeated answer, or file stability to discharge a hook witness.
func verifyCheckpointTranscript(ctx context.Context, input io.Reader, request ports.NativeCheckpointRequest) (ports.NativeCheckpointBoundary, error) {
	var evidence domain.NativeCheckpointEvidence
	if json.Unmarshal([]byte(request.Evidence), &evidence) != nil || evidence.Invalid ||
		evidence.NativeID != request.ProviderConversationID || len(evidence.Events) == 0 {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("missing or invalid owned observations")
	}
	records := make(map[string]checkpointRecord)
	wantedSubmissions := make(map[string]bool)
	for _, observation := range evidence.Events {
		if observation.Submission && observation.SubmissionID != "" {
			wantedSubmissions[domain.NativeSubmissionContext(observation.SubmissionID)] = true
		}
	}
	leaf := ""
	const maxTranscriptBytes = 64 << 20
	limited := &io.LimitedReader{R: input, N: maxTranscriptBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return ports.NativeCheckpointBoundary{}, err
		}
		var record checkpointRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("incomplete native record")
		}
		if record.Type == "system" && record.Subtype == "compact_boundary" {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("compacted native ancestry requires reconciliation")
		}
		if record.UUID == "" || record.Sidechain {
			continue
		}
		if record.SessionID != request.ProviderConversationID {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native record identity mismatch")
		}
		if _, exists := records[record.UUID]; exists {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("ambiguous native UUID")
		}
		records[record.UUID] = record
		leaf = record.UUID
	}
	if scanner.Err() != nil || limited.N == 0 {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native transcript exceeds readable bounds")
	}
	var chain []checkpointRecord
	seen := make(map[string]bool)
	for leaf != "" {
		record, found := records[leaf]
		if !found || seen[leaf] {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("incomplete native ancestry")
		}
		seen[leaf] = true
		chain = append(chain, record)
		leaf = record.ParentUUID
	}
	slices.Reverse(chain)
	var turns []checkpointTurn
	submissions := make(map[string]int)
	for _, record := range chain {
		if record.Type == "user" {
			if record.Meta && record.TurnCompanion {
				continue
			}
			if checkpointToolResults(record.Message.Content) {
				continue
			}
			if record.PromptID == "" {
				return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native user has no prompt identity")
			}
			text, err := checkpointContent(record.Message.Content)
			if err != nil {
				return ports.NativeCheckpointBoundary{}, err
			}
			// Replay never shows a note: Claude Code's own notices, or an isMeta
			// record, which getSessionMessages drops. A note inside its prompt's
			// cycle, such as an interrupt marker, adds no turn. One that starts a
			// cycle keeps its prompt for Stop evidence; the boundary skips it.
			notice := record.Meta || claudeTranscriptNotice(text)
			if notice && len(turns) > 0 && turns[len(turns)-1].promptID == record.PromptID {
				continue
			}
			turns = append(turns, checkpointTurn{promptID: record.PromptID, userID: record.UUID, user: text, notice: notice})
			continue
		}
		if len(turns) == 0 {
			continue
		}
		turn := &turns[len(turns)-1]
		switch record.Type {
		case "attachment":
			if record.Attachment.Type == "hook_additional_context" && record.Attachment.HookEvent == "UserPromptSubmit" {
				var contents []string
				if json.Unmarshal(record.Attachment.Content, &contents) != nil {
					return ports.NativeCheckpointBoundary{}, checkpointUnsettled("invalid native submission attachment")
				}
				for _, content := range contents {
					if !wantedSubmissions[content] {
						continue
					}
					if _, duplicate := submissions[content]; duplicate {
						return ports.NativeCheckpointBoundary{}, checkpointUnsettled("ambiguous native submission attachment")
					}
					submissions[content] = len(turns) - 1
				}
			}
		case "assistant":
			text, err := checkpointContent(record.Message.Content)
			if err != nil {
				return ports.NativeCheckpointBoundary{}, err
			}
			turn.assistant = text
			turn.complete = record.Message.StopReason == "end_turn"
		case "system":
			if record.Subtype == "stop_hook_summary" && record.PreventedContinuation {
				turn.complete = false
			}
		}
	}
	if len(turns) == 0 {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("no native user boundary")
	}
	byPrompt := make(map[string]int)
	for i, turn := range turns {
		if _, duplicate := byPrompt[turn.promptID]; duplicate {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("ambiguous native prompt ID")
		}
		byPrompt[turn.promptID] = i
	}
	for _, observation := range evidence.Events {
		if observation.Generation == "" {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("unowned native observation")
		}
		index, found := byPrompt[observation.PromptID]
		if observation.Submission {
			index, found = submissions[domain.NativeSubmissionContext(observation.SubmissionID)]
			if observation.SubmissionID == "" || !found ||
				(!observation.Coordination && !domain.NativeCheckpointTextMatches(observation.Text, turns[index].user)) {
				return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native submission attachment not flushed")
			}
		} else if !found {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("observed native prompt not flushed")
		} else if observation.Text != "" && !domain.NativeCheckpointTextMatches(observation.Text, turns[index].assistant) {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("Stop answer does not match its native prompt")
		}
		if !turns[index].complete {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("observed native turn not completed")
		}
	}
	latest := turns[len(turns)-1]
	if !latest.complete {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native tail still pending")
	}
	// Chat replay skips notes, so their output belongs to the turn before.
	owner := len(turns) - 1
	for owner >= 0 && turns[owner].notice {
		owner--
	}
	if owner < 0 {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("no native user boundary")
	}
	return ports.NativeCheckpointBoundary{UserMessageID: turns[owner].userID, UserText: turns[owner].user, AssistantText: latest.assistant}, nil
}

func checkpointToolResults(raw json.RawMessage) bool {
	var blocks []struct {
		Type string `json:"type"`
	}
	if json.Unmarshal(raw, &blocks) != nil || len(blocks) == 0 {
		return false
	}
	for _, block := range blocks {
		if block.Type != "tool_result" {
			return false
		}
	}
	return true
}

func checkpointContent(raw json.RawMessage) (string, error) {
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text, nil
	}
	var blocks []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &blocks) != nil {
		return "", checkpointUnsettled("unsupported native content")
	}
	var texts []string
	for _, block := range blocks {
		if block.Type == "text" {
			texts = append(texts, block.Text)
		}
	}
	return strings.Join(texts, "\n"), nil
}
