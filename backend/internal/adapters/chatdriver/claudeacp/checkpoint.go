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

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/claudecode"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var _ ports.ChatDriverReconnector = (*checkpointDriver)(nil)
var _ ports.ChatIsolatedForker = (*checkpointDriver)(nil)

type checkpointDriver struct {
	ports.ChatDriver
	plugin claudePlugin
}

func (d *checkpointDriver) Reconnect(ctx context.Context, cfg ports.ChatResumeConfig) (ports.ChatConversation, error) {
	reconnector, ok := d.ChatDriver.(ports.ChatDriverReconnector)
	if !ok {
		return nil, ports.ErrChatHostNotRunning
	}
	return reconnector.Reconnect(ctx, cfg)
}

func (d *checkpointDriver) ForkIntoHost(ctx context.Context, sourceID, lastTurnID string, cfg ports.ChatStartConfig) (ports.ChatConversation, error) {
	forker, ok := d.ChatDriver.(ports.ChatIsolatedForker)
	if !ok {
		return nil, ports.ErrChatCapabilityUnavailable
	}
	return forker.ForkIntoHost(ctx, sourceID, lastTurnID, cfg)
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

var checkpointRetainedBytes, checkpointTranscriptBytes int64 = 64 << 20, 1 << 30

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
		Prompt    json.RawMessage `json:"prompt"`
	} `json:"attachment"`
	Message struct {
		Content    json.RawMessage `json:"content"`
		StopReason string          `json:"stop_reason"`
		Model      string          `json:"model"`
	} `json:"message"`
	CompactMetadata struct {
		PreservedMessages *struct {
			AnchorUUID string   `json:"anchorUuid"`
			UUIDs      []string `json:"uuids"`
		} `json:"preservedMessages"`
		PreservedSegment *struct {
			HeadUUID   string `json:"headUuid"`
			AnchorUUID string `json:"anchorUuid"`
			TailUUID   string `json:"tailUuid"`
		} `json:"preservedSegment"`
	} `json:"compactMetadata"`
}

func (r checkpointRecord) compactBoundary() bool {
	return r.Type == "system" && r.Subtype == "compact_boundary"
}

func followsCompaction(records map[string]checkpointRecord, parent string) bool {
	for steps := 0; parent != "" && steps <= len(records); steps++ {
		record, found := records[parent]
		switch {
		case !found:
			return false
		case record.compactBoundary():
			return true
		case record.Type != "attachment":
			return false
		}
		parent = record.ParentUUID
	}
	return false
}

func relinkCompactedTranscript(records map[string]checkpointRecord, boundaries []string) map[string]string {
	ends := make(map[string]string)
	reparent := func(id, parent string) {
		record := records[id]
		record.ParentUUID = parent
		records[id] = record
	}
	for _, id := range boundaries {
		metadata := records[id].CompactMetadata
		if kept := metadata.PreservedMessages; kept != nil {
			if len(kept.UUIDs) == 0 || slices.ContainsFunc(kept.UUIDs, func(id string) bool { _, found := records[id]; return !found }) {
				continue
			}
			parent := kept.AnchorUUID
			for _, keptID := range kept.UUIDs {
				reparent(keptID, parent)
				parent = keptID
			}
			first, last := kept.UUIDs[0], kept.UUIDs[len(kept.UUIDs)-1]
			ends[kept.AnchorUUID] = last
			for recordID, record := range records {
				if record.ParentUUID == kept.AnchorUUID && recordID != first {
					reparent(recordID, last)
				}
			}
		} else if segment := metadata.PreservedSegment; segment != nil {
			if _, found := records[segment.HeadUUID]; found {
				reparent(segment.HeadUUID, segment.AnchorUUID)
				ends[segment.AnchorUUID] = segment.TailUUID
			}
			for recordID, record := range records {
				if record.ParentUUID == segment.AnchorUUID && recordID != segment.HeadUUID {
					reparent(recordID, segment.TailUUID)
				}
			}
		}
	}
	return ends
}

type checkpointTurn struct {
	promptID, userID, user, assistant string
	complete, notice                  bool
	interrupted                       bool
	carried, resumed                  bool
}

const claudeSyntheticModel = "<synthetic>"

const claudeCompactionSummary = "This session is being continued from a previous conversation that ran out of context."

func claudeTranscriptNotice(text string) bool {
	text = strings.TrimSpace(text)
	return strings.HasPrefix(text, "<task-notification>") ||
		strings.HasPrefix(text, claudeCompactionSummary) ||
		strings.HasPrefix(text, "[Request interrupted by user") && strings.HasSuffix(text, "]")
}

var claudeLocalCommandMarkers = []string{"command-name", "command-message", "command-args", "local-command-stdout", "local-command-stderr"}

func claudeLocalCommandMetadata(text string) bool {
	var rest strings.Builder
	stripped := false
	for i := 0; i < len(text); {
		matched := false
		if text[i] == '<' {
			for _, marker := range claudeLocalCommandMarkers {
				open, closing := "<"+marker+">", "</"+marker+">"
				if !strings.HasPrefix(text[i:], open) {
					continue
				}
				if end := strings.Index(text[i+len(open):], closing); end >= 0 {
					i += len(open) + end + len(closing)
					matched, stripped = true, true
				}
				break
			}
		}
		if !matched {
			rest.WriteByte(text[i])
			i++
		}
	}
	return stripped && strings.TrimSpace(rest.String()) == ""
}

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
	position := make(map[string]int)
	recordBytes := make(map[string]int64)
	uuids := make(map[string]bool)
	var retainedBytes int64
	var boundaries []string
	compacted := compactedWitnesses{prompts: make(map[string]bool), submissions: make(map[string]bool), boundary: -1}
	limited := &io.LimitedReader{R: input, N: checkpointTranscriptBytes + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 4<<20)
	for scanner.Scan() {
		if err := ctx.Err(); err != nil {
			return ports.NativeCheckpointBoundary{}, err
		}
		var record checkpointRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil {
			if limited.N == 0 {
				break
			}
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("incomplete native record")
		}
		if record.UUID == "" || record.Sidechain {
			continue
		}
		if record.SessionID != request.ProviderConversationID {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native record identity mismatch")
		}
		if uuids[record.UUID] {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("ambiguous native UUID")
		}
		uuids[record.UUID] = true
		if record.compactBoundary() {
			retainedBytes = compactNativeRecords(records, recordBytes, wantedSubmissions, &compacted, record)
			compacted.boundary = len(uuids)
			boundaries = append(boundaries, record.UUID)
		}
		records[record.UUID] = record
		position[record.UUID] = len(uuids)
		recordBytes[record.UUID] = int64(len(scanner.Bytes()))
		retainedBytes += int64(len(scanner.Bytes()))
		if retainedBytes > checkpointRetainedBytes {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native transcript exceeds readable bounds")
		}
		leaf = record.UUID
	}
	if scanner.Err() != nil || limited.N == 0 {
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native transcript exceeds readable bounds")
	}
	if end, found := relinkCompactedTranscript(records, boundaries)[leaf]; found {
		leaf = end
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
	queuedTexts := make(map[string]string)
	queuedByUUID := make(map[string]string)
	for _, record := range chain {
		if record.Type == "user" {
			if record.Meta && record.TurnCompanion {
				continue
			}
			if checkpointToolResults(record.Message.Content) {
				if len(turns) > 0 && turns[len(turns)-1].interrupted {
					turns[len(turns)-1].interrupted = false
					turns[len(turns)-1].complete = false
				}
				continue
			}
			if record.PromptID == "" {
				return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native user has no prompt identity")
			}
			text, err := checkpointContent(record.Message.Content)
			if err != nil {
				return ports.NativeCheckpointBoundary{}, err
			}
			notice := record.Meta || claudeTranscriptNotice(text) || claudeLocalCommandMetadata(text)
			if notice && len(turns) > 0 && turns[len(turns)-1].promptID == record.PromptID {
				if !record.Meta && claudecode.IsInterruptMarker(record.Message.Content) {
					turns[len(turns)-1].interrupted = true
					turns[len(turns)-1].complete = true
				}
				continue
			}
			turns = append(turns, checkpointTurn{promptID: record.PromptID, userID: record.UUID, user: text, notice: notice,
				carried: notice && followsCompaction(records, record.ParentUUID)})
			continue
		}
		if len(turns) == 0 {
			continue
		}
		turn := &turns[len(turns)-1]
		switch record.Type {
		case "attachment":
			if record.Attachment.Type == "queued_command" {
				text, err := checkpointContent(record.Attachment.Prompt)
				if err != nil {
					return ports.NativeCheckpointBoundary{}, err
				}
				queuedByUUID[record.UUID] = text
				continue
			}
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
					if queued, ok := queuedByUUID[record.ParentUUID]; ok {
						queuedTexts[content] = queued
					}
				}
			}
		case "assistant":
			if turn.carried && position[record.UUID] > compacted.boundary {
				turn.resumed = true
			}
			if turn.interrupted && record.Message.Model == claudeSyntheticModel {
				break
			}
			text, err := checkpointContent(record.Message.Content)
			if err != nil {
				return ports.NativeCheckpointBoundary{}, err
			}
			turn.interrupted = false
			turn.assistant = text
			turn.complete = record.Message.StopReason == "end_turn"
		case "system":
			if record.Subtype == "stop_hook_summary" && record.PreventedContinuation {
				turn.interrupted = false
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
			submission := domain.NativeSubmissionContext(observation.SubmissionID)
			index, found = submissions[submission]
			if !found && observation.SubmissionID != "" && compacted.submissions[submission] {
				continue
			}
			submitted := ""
			if found {
				submitted = turns[index].user
				if text, ok := queuedTexts[submission]; ok {
					submitted = text
				}
			}
			if observation.SubmissionID == "" || !found ||
				(!observation.Coordination && !domain.NativeCheckpointTextMatches(observation.Text, submitted)) {
				return ports.NativeCheckpointBoundary{}, checkpointUnsettled("native submission attachment not flushed")
			}
		} else if compacted.prompts[observation.PromptID] && (!found || turns[index].carried && !turns[index].resumed) {
			continue
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
	owner := len(turns) - 1
	for owner >= 0 && turns[owner].notice {
		owner--
	}
	if owner < 0 {
		if len(boundaries) > 0 {
			return ports.NativeCheckpointBoundary{}, checkpointUnsettled("no native user prompt since compaction")
		}
		return ports.NativeCheckpointBoundary{}, checkpointUnsettled("no native user boundary")
	}
	return ports.NativeCheckpointBoundary{
		UserMessageID: turns[owner].userID, UserText: turns[owner].user,
		AssistantText: latest.assistant, Interrupted: latest.interrupted,
	}, nil
}

type compactedWitnesses struct {
	prompts, submissions map[string]bool
	boundary             int
}

func compactNativeRecords(records map[string]checkpointRecord, recordBytes map[string]int64, wanted map[string]bool, compacted *compactedWitnesses, boundary checkpointRecord) int64 {
	for _, record := range records {
		switch {
		case record.Type == "user" && record.PromptID != "":
			compacted.prompts[record.PromptID] = true
		case record.Type == "attachment" && record.Attachment.Type == "hook_additional_context" &&
			record.Attachment.HookEvent == "UserPromptSubmit":
			var contents []string
			if json.Unmarshal(record.Attachment.Content, &contents) != nil {
				continue
			}
			for _, content := range contents {
				if wanted[content] {
					compacted.submissions[content] = true
				}
			}
		}
	}
	keep := make(map[string]bool)
	if kept := boundary.CompactMetadata.PreservedMessages; kept != nil {
		for _, id := range kept.UUIDs {
			keep[id] = true
		}
	} else if segment := boundary.CompactMetadata.PreservedSegment; segment != nil {
		for id := segment.TailUUID; id != "" && !keep[id]; id = records[id].ParentUUID {
			if _, found := records[id]; !found {
				break
			}
			keep[id] = true
			if id == segment.HeadUUID {
				break
			}
		}
	}
	var retained int64
	for id := range records {
		if keep[id] {
			retained += recordBytes[id]
			continue
		}
		delete(records, id)
		delete(recordBytes, id)
	}
	return retained
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
