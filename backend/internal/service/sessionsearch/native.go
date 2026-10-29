package sessionsearch

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// errUnsupportedHarness marks a harness whose transcripts AO does not parse.
// The session stays findable by title; coverage reports the gap. It is part of
// errorCode's vocabulary so a recorded source error names the real cause.
var errUnsupportedHarness = errors.New("harness transcripts are not indexed")

// nativeLine is one decoded transcript record that carries searchable text.
type nativeLine struct {
	kind domain.SessionSearchKind
	text string
	at   time.Time
	// ordinal disambiguates two records at the same byte offset (a record that
	// yields both a message and a tool result).
	ordinal int
	// offset is the byte offset of the line this unit came from; it is both the
	// document's seq and part of its key, so an appended line never collides.
	offset int64
	// paths are files the unit touched (a file change, an edited or written
	// file); they go in the paths column, which path: and file-name searches use.
	paths []string
}

// nativeFormat is the version of what the native readers extract. It is
// recorded on each native source; a source read by an older version is reset
// and read again from the start, because a native cursor is a byte offset and
// would otherwise never revisit text an older reader skipped.
//
//	2: Claude queued prompts and edited files; Codex reads one stream per file
//	   (no duplicate messages) and adds file changes, MCP and web tools;
//	   OpenCode v2 adds tool calls, their output and reasoning.
const nativeFormat = "native-v2"

// nativeDoc turns one parsed unit into a document.
func nativeDoc(sourceKey string, target domain.SessionSearchTarget, machine string, line nativeLine, now time.Time) (domain.SessionSearchDoc, bool) {
	text := line.text
	if line.kind == domain.SessionSearchKindCommand {
		// A failing command is read from its end.
		text = tailText(text, maxDocBodyBytes)
	}
	body := searchableText(text)
	paths := searchableText(strings.Join(line.paths, "\n"))
	if strings.TrimSpace(body) == "" && paths == "" {
		return domain.SessionSearchDoc{}, false
	}
	created := line.at
	if created.IsZero() {
		created = target.CreatedAt
	}
	return domain.SessionSearchDoc{
		SourceKey: sourceKey,
		DocKey:    fmt.Sprintf("line:%d:%d", line.offset, line.ordinal),
		SessionID: string(target.ID),
		ProjectID: string(target.ProjectID),
		Harness:   string(target.Harness),
		Machine:   machine,
		Kind:      line.kind,
		Seq:       line.offset,
		Body:      body,
		Paths:     paths,
		CreatedAt: created,
		IndexedAt: now,
	}, true
}

// indexNativeSession indexes one session's provider transcript, if the harness
// has one AO understands. It returns true when it wrote documents.
//
// The transcript is opened read-only and read forward from the stored byte
// offset. A file whose identity or size went backwards was rewritten or
// truncated, so the source restarts from zero instead of reading from a stale
// offset.
func (s *Service) indexNativeSession(ctx context.Context, target domain.SessionSearchTarget) bool {
	sourceKey := NativeSourceKey(target.Harness, string(target.ID))
	// OpenCode keeps its history in its own database rather than a transcript,
	// so it is read through its own path.
	switch target.Harness {
	case domain.HarnessOpenCode:
		return s.indexOpenCodeSession(ctx, target, s.roots.OpenCodeData)
	case domain.HarnessOpenCodeV2:
		return s.indexOpenCodeSession(ctx, target, s.roots.OpenCodeV2Data)
	}
	if !nativeHarnessSupported(target.Harness) {
		s.markUnsupported(ctx, sourceKey, target)
		return false
	}
	previous, hadPrevious, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		s.log.Warn("session search source read failed", "source", sourceKey, "error", err)
		return false
	}
	// Reuse the recorded path when it still exists. Resolving from scratch globs
	// the provider's whole transcript tree, and doing that per session on every
	// 30-second sweep is the single most expensive thing an idle sweep can do.
	path := ""
	if hadPrevious && previous.Path != "" {
		for _, root := range s.transcriptRootsFor(target.Harness) {
			safe, ok := withinRoot(root, previous.Path)
			if !ok {
				continue
			}
			if info, statErr := os.Stat(safe); statErr == nil && info.Mode().IsRegular() {
				path = safe
				break
			}
		}
	}
	if path == "" {
		resolved, resolveErr := s.locateTranscript(target)
		if resolveErr != nil {
			s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, resolveErr)
			return false
		}
		path = resolved
	}
	if path == "" {
		// No transcript on disk is normal for a Chat session or a session whose
		// workspace is gone. Record that once so later sweeps do not re-glob the
		// provider's whole tree for a session that has nothing to read, then keep
		// any documents already indexed.
		s.recordMissingTranscript(ctx, sourceKey, target, hadPrevious, previous)
		return false
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, statErr)
		return false
	}
	// A source read by an older reader, or a Codex rollout that started writing
	// its item stream after it was first read, is read again from the start.
	parser, format := nativeParserFor(target.Harness, path)
	formatChanged := hadPrevious && previous.Fingerprint != format
	offset := int64(0)
	if hadPrevious && !formatChanged && previous.Path == path && previous.FileSize <= info.Size() {
		offset = previous.CursorValue
	}
	if hadPrevious && (formatChanged || previous.Path != path || previous.FileSize > info.Size()) {
		// The transcript was replaced or truncated, or an older reader indexed
		// it: its earlier documents describe text this pass will not rewrite.
		if err := s.store.DeleteSessionSearchDocsForSource(ctx, sourceKey); err != nil {
			s.log.Warn("session search reset failed", "source", sourceKey, "error", err)
			return false
		}
	}
	if offset >= info.Size() && hadPrevious && previous.State == domain.SessionSearchSourceActive {
		return false
	}
	lines, nextOffset, readErr := readNativeTranscript(ctx, path, offset, parser)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, readErr)
		return false
	}
	now := s.now()
	docs := make([]domain.SessionSearchDoc, 0, len(lines)+1)
	for _, line := range lines {
		if doc, ok := nativeDoc(sourceKey, target, s.machine, line, now); ok {
			docs = append(docs, doc)
		}
	}
	// The session's own name and prompt make it findable even when the provider
	// transcript holds nothing (or the session is a TUI session whose transcript
	// AO could not parse).
	if title := strings.TrimSpace(target.DisplayName); title != "" {
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: sourceKey,
			DocKey:    "title",
			SessionID: string(target.ID),
			ProjectID: string(target.ProjectID),
			Harness:   string(target.Harness),
			Machine:   s.machine,
			Kind:      domain.SessionSearchKindTitle,
			Title:     truncateBytes(title, maxTitleBytes),
			Body:      searchableText(firstNonEmpty(target.LatestPrompt, target.Prompt)),
			CreatedAt: target.CreatedAt,
			IndexedAt: now,
		})
	}
	if err := s.writeDocs(ctx, docs); err != nil {
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, err)
		return false
	}
	count, err := s.store.CountSessionSearchDocsForSource(ctx, sourceKey)
	if err != nil {
		s.log.Warn("session search count failed", "source", sourceKey, "error", err)
	}
	source := domain.SessionSearchSource{
		SourceKey:   sourceKey,
		Kind:        domain.SessionSearchSourceNative,
		SessionID:   string(target.ID),
		ProjectID:   string(target.ProjectID),
		Harness:     string(target.Harness),
		Path:        path,
		CursorValue: nextOffset,
		FileSize:    info.Size(),
		FileMtime:   info.ModTime().UnixNano(),
		Fingerprint: format,
		DocCount:    count,
		State:       domain.SessionSearchSourceActive,
		UpdatedAt:   now,
	}
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		s.log.Warn("session search source update failed", "source", sourceKey, "error", err)
		return false
	}
	return len(docs) > 0
}

// markUnsupported records that a harness has no parsed transcript, once, and
// still indexes the session's own name and prompt. "AO cannot read this
// harness's transcript" must not mean "this session cannot be found": the title
// is the one thing AO always knows.
func (s *Service) markUnsupported(ctx context.Context, sourceKey string, target domain.SessionSearchTarget) {
	previous, had, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		return
	}
	if had && previous.State == domain.SessionSearchSourceUnsupported && previous.DocCount > 0 {
		return
	}
	s.writeTitleDoc(ctx, sourceKey, target)
	source := domain.SessionSearchSource{
		SourceKey: sourceKey,
		Kind:      domain.SessionSearchSourceNative,
		SessionID: string(target.ID),
		ProjectID: string(target.ProjectID),
		Harness:   string(target.Harness),
		State:     domain.SessionSearchSourceUnsupported,
		UpdatedAt: s.now(),
	}
	if had {
		source = previous
		source.State = domain.SessionSearchSourceUnsupported
		source.UpdatedAt = s.now()
	}
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		s.log.Warn("session search source update failed", "source", sourceKey, "error", err)
	}
}

// recordMissingTranscript remembers that a session has no resolvable transcript.
//
// A Chat session has no native transcript, and a TUI session whose workspace was
// cleaned up may never have one again. Without this record every sweep re-globs
// the provider tree for each such session, which is the most expensive thing an
// idle sweep can do. A recorded miss is re-checked only when the source is
// marked pending again, so a transcript that appears later is still found.
func (s *Service) recordMissingTranscript(ctx context.Context, sourceKey string, target domain.SessionSearchTarget, hadPrevious bool, previous domain.SessionSearchSource) {
	if hadPrevious && previous.State == domain.SessionSearchSourceNoTranscript {
		return
	}
	source := previous
	source.SourceKey = sourceKey
	source.Kind = domain.SessionSearchSourceNative
	source.SessionID = string(target.ID)
	source.ProjectID = string(target.ProjectID)
	source.Harness = string(target.Harness)
	source.State = domain.SessionSearchSourceNoTranscript
	source.UpdatedAt = s.now()
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		s.log.Warn("session search source update failed", "source", sourceKey, "error", err)
	}
}

// transcriptRootsFor lists the provider roots a harness's transcripts may live
// under. Codex has two (live and archived), so a recorded path is checked
// against both rather than against one root that would reject a valid archive.
func (s *Service) transcriptRootsFor(harness domain.AgentHarness) []string {
	switch harness {
	case domain.HarnessClaudeCode:
		return []string{s.roots.ClaudeProjects}
	case domain.HarnessCodex:
		return []string{s.roots.CodexSessions, s.roots.CodexArchived}
	default:
		return nil
	}
}

// locateTranscript resolves the provider transcript for a session, preferring
// the durable path AO recorded and falling back to the provider's own naming
// convention. Any resolved path must stay inside the configured root.
func (s *Service) locateTranscript(target domain.SessionSearchTarget) (string, error) {
	switch target.Harness {
	case domain.HarnessClaudeCode:
		return s.locateClaudeTranscript(string(target.ID), target.WorkspacePath)
	case domain.HarnessCodex:
		return s.locateCodexTranscript(string(target.ID))
	case domain.HarnessOpenCode, domain.HarnessOpenCodeV2:
		// OpenCode history lives in its own SQLite store, read by
		// indexOpenCodeSession rather than by a JSONL parser.
		return "", nil
	default:
		return "", nil
	}
}

// locateClaudeTranscript finds ~/.claude/projects/<project-key>/<native-id>.jsonl.
// Claude names the project key from the workspace path, so the session's own
// workspace narrows the search before any glob runs.
func (s *Service) locateClaudeTranscript(nativeID, workspacePath string) (string, error) {
	root := strings.TrimSpace(s.roots.ClaudeProjects)
	if root == "" || nativeID == "" {
		return "", nil
	}
	candidates := make([]string, 0, 2)
	if workspacePath != "" {
		key := claudeProjectKey(workspacePath)
		candidates = append(candidates, filepath.Join(root, key, nativeID+".jsonl"))
	}
	matches, err := filepath.Glob(filepath.Join(root, "*", nativeID+".jsonl"))
	if err != nil {
		return "", err
	}
	candidates = append(candidates, matches...)
	for _, candidate := range candidates {
		if safe, ok := withinRoot(root, candidate); ok {
			if info, statErr := os.Stat(safe); statErr == nil && info.Mode().IsRegular() {
				return safe, nil
			}
		}
	}
	return "", nil
}

// claudeProjectKey mirrors Claude's directory naming: the absolute workspace
// path with every separator and dot replaced by a dash.
func claudeProjectKey(workspacePath string) string {
	cleaned := filepath.Clean(workspacePath)
	var key strings.Builder
	for _, character := range cleaned {
		switch character {
		case '/', '\\', '.', ':':
			key.WriteByte('-')
		default:
			key.WriteRune(character)
		}
	}
	return key.String()
}

// locateCodexTranscript finds ~/.codex/sessions/**/rollout-*-<native-id>.jsonl
// (or the archived equivalent). Codex's id is the rollout file's suffix.
func (s *Service) locateCodexTranscript(nativeID string) (string, error) {
	if nativeID == "" {
		return "", nil
	}
	roots := []string{strings.TrimSpace(s.roots.CodexSessions), strings.TrimSpace(s.roots.CodexArchived)}
	var candidates []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		pattern := filepath.Join(root, "*", "*", "*", "*"+nativeID+"*.jsonl")
		matches, err := filepath.Glob(pattern)
		if err != nil {
			return "", err
		}
		candidates = append(candidates, matches...)
	}
	sort.Strings(candidates)
	for _, candidate := range candidates {
		root := ""
		for _, configured := range roots {
			if configured != "" && strings.HasPrefix(candidate, configured+string(filepath.Separator)) {
				root = configured
				break
			}
		}
		if root == "" {
			continue
		}
		safe, ok := withinRoot(root, candidate)
		if !ok {
			continue
		}
		info, statErr := os.Stat(safe)
		if statErr != nil || !info.Mode().IsRegular() {
			continue
		}
		return safe, nil
	}
	return "", nil
}

// withinRoot refuses a path that escapes the provider root, including through a
// symlinked parent directory. The indexer only ever reads what a provider wrote
// under its own directory.
func withinRoot(root, candidate string) (string, bool) {
	cleanRoot := filepath.Clean(root)
	cleanCandidate := filepath.Clean(candidate)
	if !strings.HasPrefix(cleanCandidate, cleanRoot+string(filepath.Separator)) {
		return "", false
	}
	resolvedRoot, err := filepath.EvalSymlinks(cleanRoot)
	if err != nil {
		return "", false
	}
	resolvedCandidate, err := filepath.EvalSymlinks(cleanCandidate)
	if err != nil {
		return "", false
	}
	if !strings.HasPrefix(resolvedCandidate, resolvedRoot+string(filepath.Separator)) {
		return "", false
	}
	return resolvedCandidate, true
}

// nativeParser decodes one transcript record into searchable units. A record may
// carry more than one (a message and its tool call), and returns nil when it
// carries none (a snapshot, a mode change).
type nativeParser func(line []byte) []nativeLine

// nativeParserFor returns the parser for a harness, or nil when AO does not
// parse that harness's transcript, and the format it reads with. A Codex
// rollout read from its response_item stream before its first item_completed
// line was written changes format once that line appears, which resets the
// source instead of indexing its first turns from both streams.
func nativeParserFor(harness domain.AgentHarness, path string) (nativeParser, string) {
	switch harness {
	case domain.HarnessClaudeCode:
		return parseClaudeTranscriptLine, nativeFormat
	case domain.HarnessCodex:
		if codexHasItemStream(path) {
			return codexParser(true), nativeFormat + "+items"
		}
		return codexParser(false), nativeFormat
	default:
		return nil, ""
	}
}

// nativeHarnessSupported reports whether AO reads this harness's transcript file.
func nativeHarnessSupported(harness domain.AgentHarness) bool {
	return harness == domain.HarnessClaudeCode || harness == domain.HarnessCodex
}

// parseClaudeTranscriptLine decodes a Claude Code JSONL record.
//
// It indexes the whole conversation, not just the prose: text parts, tool calls
// (name plus arguments), and tool results. Sidechain (subagent) records are
// skipped because they are not part of the conversation the user saw, and meta
// records carry harness caveats rather than conversation.
func parseClaudeTranscriptLine(line []byte) []nativeLine {
	var record struct {
		Type        string          `json:"type"`
		IsSidechain bool            `json:"isSidechain"`
		IsMeta      bool            `json:"isMeta"`
		Timestamp   string          `json:"timestamp"`
		Message     json.RawMessage `json:"message"`
	}
	if json.Unmarshal(line, &record) != nil {
		return nil
	}
	if record.IsSidechain || record.IsMeta {
		return nil
	}
	if record.Type == "attachment" {
		return claudeAttachmentUnits(line, record.Timestamp)
	}
	if record.Type != "user" && record.Type != "assistant" {
		return nil
	}
	var message struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	}
	if json.Unmarshal(record.Message, &message) != nil {
		return nil
	}
	at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	units := claudeContentUnits(message.Content, record.Type)
	if len(units) == 0 {
		return nil
	}
	lines := make([]nativeLine, 0, len(units))
	for index, unit := range units {
		unit.at = at
		unit.ordinal = index
		lines = append(lines, unit)
	}
	return lines
}

// claudeAttachmentUnits reads the attachments that carry conversation. Most
// attachments are the harness's own bookkeeping (token reminders, tool lists,
// environment notes) and stay out. Two are not: a prompt the person queued while
// the agent was busy is delivered inside their turn but never written as a user
// record, so without this their own words were unsearchable; and a file they
// edited during the session is a path they will search for.
func claudeAttachmentUnits(line []byte, timestamp string) []nativeLine {
	var record struct {
		Attachment struct {
			Type     string          `json:"type"`
			Prompt   json.RawMessage `json:"prompt"`
			Filename string          `json:"filename"`
			Snippet  string          `json:"snippet"`
		} `json:"attachment"`
	}
	if json.Unmarshal(line, &record) != nil {
		return nil
	}
	at, _ := time.Parse(time.RFC3339Nano, timestamp)
	switch record.Attachment.Type {
	case "queued_command":
		prompt := rawJSONText(record.Attachment.Prompt)
		if strings.TrimSpace(prompt) == "" {
			return nil
		}
		return []nativeLine{{kind: domain.SessionSearchKindUser, text: prompt, at: at}}
	case "edited_text_file":
		if strings.TrimSpace(record.Attachment.Filename) == "" {
			return nil
		}
		return []nativeLine{{
			kind: domain.SessionSearchKindPath, text: record.Attachment.Filename + "\n" + record.Attachment.Snippet,
			at: at, paths: []string{record.Attachment.Filename},
		}}
	default:
		return nil
	}
}

// claudeContentUnits splits one Claude message into searchable units: prose is a
// message (tiered by the record's own role), a tool call is a tool unit, and a
// tool result is a command unit — its output is what a reader greps for when a
// build failed.
func claudeContentUnits(raw json.RawMessage, role string) []nativeLine {
	proseKind := domain.SessionSearchKindAssistant
	if role == "user" {
		proseKind = domain.SessionSearchKindUser
	}
	if len(raw) == 0 {
		return nil
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return []nativeLine{{kind: proseKind, text: text}}
	}
	var parts []struct {
		Type     string          `json:"type"`
		Text     string          `json:"text"`
		Thinking string          `json:"thinking"`
		Name     string          `json:"name"`
		Input    json.RawMessage `json:"input"`
		Content  json.RawMessage `json:"content"`
	}
	if json.Unmarshal(raw, &parts) != nil {
		return nil
	}
	var units []nativeLine
	for _, part := range parts {
		switch part.Type {
		case "text":
			if strings.TrimSpace(part.Text) != "" {
				units = append(units, nativeLine{kind: proseKind, text: part.Text})
			}
		case "thinking":
			if strings.TrimSpace(part.Thinking) != "" {
				units = append(units, nativeLine{kind: domain.SessionSearchKindAssistant, text: part.Thinking})
			}
		case "tool_use":
			body := strings.TrimSpace(part.Name + "\n" + rawJSONText(part.Input))
			if body != "" {
				units = append(units, nativeLine{kind: domain.SessionSearchKindTool, text: body})
			}
		case "tool_result":
			body := rawJSONText(part.Content)
			if strings.TrimSpace(body) != "" {
				units = append(units, nativeLine{kind: domain.SessionSearchKindCommand, text: body})
			}
		}
	}
	return units
}

// codexItemStreamProbeBytes is how much of a rollout codexHasItemStream reads.
// The item stream starts with the first turn, so it is near the top of a file
// that has one.
const codexItemStreamProbeBytes = 256 << 10

// codexHasItemStream reports whether a rollout records Codex's item stream
// (event_msg item_completed), which newer Codex writes next to the older
// response_item stream.
func codexHasItemStream(path string) bool {
	file, err := os.Open(path) //nolint:gosec // provider-owned transcript root, validated by withinRoot
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	buffer := make([]byte, codexItemStreamProbeBytes)
	n, _ := io.ReadFull(file, buffer)
	return bytes.Contains(buffer[:n], []byte(`"item_completed"`))
}

// codexParser reads one Codex rollout.
//
// Newer Codex writes every message and command twice: once in the item stream
// (event_msg item_completed, what its UI shows) and once as a response_item.
// Reading both indexed each one twice, which doubled a session's match count
// and let one message outrank another by repetition. A rollout with an item
// stream is read from that stream only: its user messages are what the person
// typed (the response_item ones also carry injected environment and
// instructions), and it is the stream with file changes and MCP calls. A rollout
// without one (older Codex) is read from its response_items.
func codexParser(itemStream bool) nativeParser {
	return func(line []byte) []nativeLine { return parseCodexTranscriptLine(line, itemStream) }
}

// parseCodexTranscriptLine decodes one Codex rollout record from the stream the
// rollout is read from (see codexParser). Developer and system roles are
// skipped: they are instructions, not conversation.
func parseCodexTranscriptLine(line []byte, itemStream bool) []nativeLine {
	var record struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil {
		return nil
	}
	at, _ := time.Parse(time.RFC3339Nano, record.Timestamp)
	switch {
	case record.Type == "event_msg" && itemStream:
		return codexItemUnits(record.Payload, at)
	case record.Type == "response_item" && !itemStream:
		return codexResponseUnits(record.Payload, at)
	default:
		return nil
	}
}

// codexResponseUnits reads the response_item stream of an older rollout.
func codexResponseUnits(raw json.RawMessage, at time.Time) []nativeLine {
	var payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Summary []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"summary"`
		// Function-call style tool invocations.
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
		Input     json.RawMessage `json:"input"`
		Output    string          `json:"output"`
	}
	if json.Unmarshal(raw, &payload) != nil {
		return nil
	}
	switch payload.Type {
	case "message":
		if payload.Role != "user" && payload.Role != "assistant" {
			return nil
		}
		kind := domain.SessionSearchKindAssistant
		if payload.Role == "user" {
			kind = domain.SessionSearchKindUser
		}
		if body := contentText(payload.Content); body != "" {
			return []nativeLine{{kind: kind, text: body, at: at}}
		}
	case "reasoning":
		// Reasoning is usually encrypted; its summary, when present, is prose.
		if body := contentText(payload.Summary); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindAssistant, text: body, at: at}}
		}
	case "function_call", "custom_tool_call":
		args := payload.Arguments
		if len(args) == 0 {
			args = payload.Input
		}
		if body := strings.TrimSpace(payload.Name + "\n" + rawJSONText(args)); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindTool, text: body, at: at}}
		}
	case "function_call_output", "custom_tool_call_output":
		if strings.TrimSpace(payload.Output) != "" {
			return []nativeLine{{kind: domain.SessionSearchKindCommand, text: payload.Output, at: at}}
		}
	}
	return nil
}

// codexItemUnits reads one item of the item stream.
func codexItemUnits(raw json.RawMessage, at time.Time) []nativeLine {
	var payload struct {
		Type string `json:"type"`
		Item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			// Command is an argv array in current Codex, a string in older ones.
			Command          json.RawMessage            `json:"command"`
			AggregatedOutput string                     `json:"aggregated_output"`
			Output           string                     `json:"output"`
			SummaryText      []string                   `json:"summary_text"`
			RawContent       json.RawMessage            `json:"raw_content"`
			Changes          map[string]codexFileChange `json:"changes"`
			Server           string                     `json:"server"`
			Tool             string                     `json:"tool"`
			Arguments        json.RawMessage            `json:"arguments"`
			Result           json.RawMessage            `json:"result"`
			Error            json.RawMessage            `json:"error"`
			Query            string                     `json:"query"`
			Action           json.RawMessage            `json:"action"`
			Results          json.RawMessage            `json:"results"`
		} `json:"item"`
	}
	if json.Unmarshal(raw, &payload) != nil || payload.Type != "item_completed" {
		return nil
	}
	item := payload.Item
	switch item.Type {
	case "UserMessage", "AgentMessage":
		kind := domain.SessionSearchKindAssistant
		if item.Type == "UserMessage" {
			kind = domain.SessionSearchKindUser
		}
		if body := contentText(item.Content); body != "" {
			return []nativeLine{{kind: kind, text: body, at: at}}
		}
	case "CommandExecution":
		output := item.AggregatedOutput
		if output == "" {
			output = item.Output
		}
		if body := strings.TrimSpace(codexCommandText(item.Command) + "\n" + output); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindCommand, text: body, at: at}}
		}
	case "Reasoning":
		body := strings.TrimSpace(strings.Join(item.SummaryText, "\n\n") + "\n" + rawJSONText(item.RawContent))
		if body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindAssistant, text: body, at: at}}
		}
	case "FileChange":
		return codexFileChangeUnits(item.Changes, at)
	case "McpToolCall":
		body := strings.TrimSpace(strings.Join([]string{
			item.Server, item.Tool, rawJSONText(item.Arguments), rawJSONText(item.Result), rawJSONText(item.Error),
		}, "\n"))
		if body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindTool, text: body, at: at}}
		}
	case "Extension":
		// Web search and fetch: the query, the addresses and the result titles
		// are what a person remembers. Most Extension items carry no text.
		if body := strings.TrimSpace(item.Query + "\n" + rawJSONText(item.Action) + "\n" + rawJSONText(item.Results)); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindTool, text: body, at: at}}
		}
	}
	return nil
}

// codexCommandText renders a CommandExecution's command. Current Codex records
// the argv it ran (`["/bin/zsh", "-lc", "git status"]`); the script after a
// shell's -c/-lc is what the agent wrote, so only that is kept.
func codexCommandText(raw json.RawMessage) string {
	var argv []string
	if json.Unmarshal(raw, &argv) == nil {
		if len(argv) >= 3 && (argv[len(argv)-2] == "-lc" || argv[len(argv)-2] == "-c") {
			return argv[len(argv)-1]
		}
		return strings.Join(argv, " ")
	}
	var command string
	if json.Unmarshal(raw, &command) == nil {
		return command
	}
	return ""
}

// codexFileChange is one file in a Codex FileChange item: an update carries a
// unified diff, an added file its content.
type codexFileChange struct {
	Type        string `json:"type"`
	UnifiedDiff string `json:"unified_diff"`
	Content     string `json:"content"`
	MovePath    string `json:"move_path"`
}

// codexFileChangeUnits indexes a FileChange the way AO Chat indexes one: the
// paths go in the paths column, and the body is the lines that changed (the
// symbol someone added or removed), not the surrounding context.
func codexFileChangeUnits(changes map[string]codexFileChange, at time.Time) []nativeLine {
	if len(changes) == 0 {
		return nil
	}
	paths := make([]string, 0, len(changes))
	for path := range changes {
		paths = append(paths, path)
	}
	sort.Strings(paths)
	var body strings.Builder
	var touched []string
	for _, path := range paths {
		change := changes[path]
		touched = append(touched, path)
		if change.MovePath != "" {
			touched = append(touched, change.MovePath)
		}
		body.WriteString(path)
		body.WriteString("\n")
		switch {
		case change.UnifiedDiff != "":
			body.WriteString(changedDiffLines(change.UnifiedDiff))
		case change.Content != "":
			body.WriteString(change.Content)
		}
		body.WriteString("\n")
	}
	return []nativeLine{{kind: domain.SessionSearchKindPath, text: body.String(), at: at, paths: touched}}
}

// changedDiffLines keeps the added and removed lines of a unified diff, without
// their markers and without the file headers.
func changedDiffLines(diff string) string {
	var out strings.Builder
	for _, line := range strings.Split(diff, "\n") {
		if strings.HasPrefix(line, "+++") || strings.HasPrefix(line, "---") {
			continue
		}
		if strings.HasPrefix(line, "+") || strings.HasPrefix(line, "-") {
			out.WriteString(line[1:])
			out.WriteString("\n")
		}
	}
	return out.String()
}

// contentText joins the text parts of a provider content array.
func contentText(parts []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	var builder strings.Builder
	for _, part := range parts {
		switch part.Type {
		case "input_text", "output_text", "summary_text", "text", "Text":
		default:
			continue
		}
		if strings.TrimSpace(part.Text) == "" {
			continue
		}
		if builder.Len() > 0 {
			builder.WriteString("\n\n")
		}
		builder.WriteString(part.Text)
	}
	return builder.String()
}

// rawJSONText renders a JSON value as searchable text. A string is unquoted; an
// object or array is flattened to its string leaves, which is what makes a tool
// call's arguments findable by their values.
func rawJSONText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var text string
	if json.Unmarshal(raw, &text) == nil {
		return text
	}
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return string(raw)
	}
	var builder strings.Builder
	flattenJSON(&builder, value)
	return builder.String()
}

func flattenJSON(builder *strings.Builder, value any) {
	switch typed := value.(type) {
	case nil:
		return
	case string:
		if strings.TrimSpace(typed) != "" {
			if builder.Len() > 0 {
				builder.WriteString("\n")
			}
			builder.WriteString(typed)
		}
	case bool:
		return
	case float64:
		return
	case []any:
		for _, item := range typed {
			flattenJSON(builder, item)
		}
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for key := range typed {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			flattenJSON(builder, typed[key])
		}
	}
}

// readNativeTranscript reads complete lines from offset forward and returns the
// offset of the last complete line. A trailing partial line is left unread for
// the next pass, exactly as the usage parser does: a half-written record must
// not be indexed as if it were final.
func readNativeTranscript(ctx context.Context, path string, offset int64, parser nativeParser) ([]nativeLine, int64, error) {
	file, err := os.Open(path) //nolint:gosec // provider-owned transcript root, validated by withinRoot
	if err != nil {
		return nil, offset, err
	}
	defer func() { _ = file.Close() }()
	if offset > 0 {
		if _, err := file.Seek(offset, io.SeekStart); err != nil {
			return nil, offset, err
		}
	}
	reader := bufio.NewReaderSize(file, nativeReadChunk)
	lines := make([]nativeLine, 0, 128)
	consumed := offset
	for {
		if err := ctx.Err(); err != nil {
			return lines, consumed, err
		}
		raw, readErr := reader.ReadBytes('\n')
		if errors.Is(readErr, io.EOF) {
			// A record without its terminating newline is still being written.
			return lines, consumed, nil
		}
		if readErr != nil {
			return lines, consumed, readErr
		}
		lineOffset := consumed
		consumed += int64(len(raw))
		for _, unit := range parser(raw) {
			unit.offset = lineOffset
			unit.text = boundNativeText(unit.kind, unit.text)
			lines = append(lines, unit)
		}
	}
}

// boundNativeText trims a unit to what its document will keep (the end of
// command output, the start of anything else) as the transcript is read, and
// copies it so the line it came from can be freed. A transcript of tens of
// megabytes of tool output then holds a few kilobytes per unit, not the file.
func boundNativeText(kind domain.SessionSearchKind, text string) string {
	if len(text) <= maxDocBodyBytes {
		return text
	}
	if kind == domain.SessionSearchKindCommand {
		return strings.Clone(tailText(text, maxDocBodyBytes))
	}
	return strings.Clone(truncateBytes(text, maxDocBodyBytes))
}
