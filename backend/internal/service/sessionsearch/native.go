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
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

var errUnsupportedHarness = errors.New("harness transcripts are not indexed")

type nativeLine struct {
	kind                              domain.SessionSearchKind
	text                              string
	at                                time.Time
	ordinal                           int
	offset                            int64
	paths                             []string
	itemStream, turnStart, itemRecord bool
}

const nativeFormat = "native-v6"

func nativeDocs(sourceKey string, target domain.SessionSearchTarget, line nativeLine, now time.Time) []domain.SessionSearchDoc {
	var chunks []string
	switch line.kind {
	case domain.SessionSearchKindCommand, domain.SessionSearchKindTool, domain.SessionSearchKindPath:
		chunks = []string{line.text}
	default:
		chunks = textChunks(line.text, maxDocBodyBytes, chunkOverlapBytes, maxMessageChunks)
	}
	paths := searchableText(strings.Join(line.paths, "\n"))
	created := line.at
	if created.IsZero() {
		created = target.CreatedAt
	}
	docs := make([]domain.SessionSearchDoc, 0, len(chunks))
	for index, chunk := range chunks {
		body := domain.SessionSearchSplitIdentifiers(chunk, maxIdentifierSplits)
		chunkPaths := ""
		if index == 0 {
			chunkPaths = paths
		}
		if strings.TrimSpace(body) == "" && chunkPaths == "" {
			continue
		}
		docs = append(docs, domain.SessionSearchDoc{
			SourceKey: sourceKey,
			DocKey:    chunkDocKey(fmt.Sprintf("line:%d:%d", line.offset, line.ordinal), index),
			SessionID: string(target.ID),
			ProjectID: string(target.ProjectID),
			Harness:   string(target.Harness),
			Kind:      line.kind,
			Seq:       line.offset,
			Body:      body,
			Paths:     chunkPaths,
			CreatedAt: created,
			IndexedAt: now,
		})
	}
	return docs
}

type nativeOutcome int

const (
	nativeSkip nativeOutcome = iota
	nativeOpenCode
	nativeUnsupported
	nativeMissing
	nativeFailed
	nativeRead
)

type nativePlan struct {
	target     domain.SessionSearchTarget
	sourceKey  string
	outcome    nativeOutcome
	err        error
	previous   domain.SessionSearchSource
	path       string
	info       os.FileInfo
	format     string
	reset      bool
	upToDate   bool
	nextOffset int64
	docs       []domain.SessionSearchDoc
	now        time.Time
}

func (s *Service) planNativeSessionSafely(ctx context.Context, target domain.SessionSearchTarget) (plan nativePlan) {
	defer func() {
		if recovered := recover(); recovered != nil {
			plan = nativePlan{
				target:    target,
				sourceKey: NativeSourceKey(target.Harness, string(target.ID)),
				outcome:   nativeFailed,
				err:       fmt.Errorf("read transcript: panic: %v", recovered),
			}
		}
	}()
	return s.planNativeSession(ctx, target)
}

func (s *Service) planNativeSession(ctx context.Context, target domain.SessionSearchTarget) nativePlan {
	plan := nativePlan{target: target, sourceKey: NativeSourceKey(target.Harness, string(target.ID))}
	switch target.Harness {
	case domain.HarnessOpenCode, domain.HarnessOpenCodeV2:
		plan.outcome = nativeOpenCode
		return plan
	}
	if !nativeHarnessSupported(target.Harness) {
		plan.outcome = nativeUnsupported
		return plan
	}
	previous, hadPrevious, err := s.store.GetSessionSearchSource(ctx, plan.sourceKey)
	if err != nil {
		s.log.Warn("session search source read failed", "source", plan.sourceKey, "error", err)
		return plan
	}
	plan.previous = previous
	if hadPrevious && previous.State == domain.SessionSearchSourceNoTranscript &&
		previous.Fingerprint == nativeIdentity(target) && s.now().Sub(previous.UpdatedAt) < nativeMissRecheck {
		_, reported := s.recordedTranscript(target)
		if !reported && !regularFile(previous.Path) {
			plan.outcome = nativeSkip
			return plan
		}
	}
	path := ""
	if recorded, ok := s.recordedTranscript(target); ok {
		path = recorded
	} else if hadPrevious && previous.Path != "" && transcriptNamesSession(previous.Path, target) {
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
			plan.outcome, plan.err = nativeFailed, resolveErr
			return plan
		}
		path = resolved
	}
	if path == "" {
		plan.outcome = nativeMissing
		return plan
	}
	info, statErr := os.Stat(path)
	if statErr != nil {
		plan.outcome, plan.err = nativeFailed, statErr
		return plan
	}
	plan.path, plan.info = path, info
	known := ""
	if hadPrevious && previous.Path == path && (previous.FileSize == info.Size() || previous.Fingerprint == nativeFormat+"+items") {
		known = previous.Fingerprint
	}
	parser, format := nativeParserFor(target.Harness, path, known, info.Size())
	plan.format = format
	formatChanged := hadPrevious && previous.Fingerprint != format
	offset := int64(0)
	if hadPrevious && !formatChanged && previous.Path == path && previous.FileSize <= info.Size() {
		offset = previous.CursorValue
	}
	if target.Harness == domain.HarnessCodex && format == nativeFormat && offset < info.Size() && codexHasItemStreamFrom(path, offset, info.Size()) {
		parser, format = codexParser(true), nativeFormat+"+items"
		plan.format = format
		formatChanged = hadPrevious && previous.Fingerprint != format
		offset = 0
	}
	bothStreams := target.Harness == domain.HarnessCodex && format == nativeFormat+"+items" && offset == 0
	if bothStreams {
		parser = codexBothStreams
	}
	plan.reset = hadPrevious && (formatChanged || previous.Path != path || previous.FileSize > info.Size())
	if offset >= info.Size() && hadPrevious && previous.State == domain.SessionSearchSourceActive {
		plan.upToDate = true
		plan.outcome = nativeRead
		return plan
	}
	lines, nextOffset, readErr := readNativeTranscript(ctx, path, offset, info.Size(), parser)
	if readErr != nil && !errors.Is(readErr, io.EOF) {
		plan.outcome, plan.err = nativeFailed, readErr
		return plan
	}
	if bothStreams {
		lines = keepOneCodexStream(lines)
	}
	plan.nextOffset = nextOffset
	plan.now = s.now()
	docs := make([]domain.SessionSearchDoc, 0, len(lines)+1)
	for _, line := range lines {
		docs = append(docs, nativeDocs(plan.sourceKey, target, line, plan.now)...)
	}
	if title, ok := s.sessionTitleDoc(plan.sourceKey, target); ok {
		docs = append(docs, title)
	}
	plan.docs = docs
	plan.outcome = nativeRead
	return plan
}

func (s *Service) applyNativePlan(ctx context.Context, plan nativePlan) bool {
	target, sourceKey := plan.target, plan.sourceKey
	switch plan.outcome {
	case nativeSkip:
		return false
	case nativeOpenCode:
		if target.Harness == domain.HarnessOpenCodeV2 {
			return s.indexOpenCodeSession(ctx, target, s.roots.OpenCodeV2Data)
		}
		return s.indexOpenCodeSession(ctx, target, s.roots.OpenCodeData)
	case nativeUnsupported:
		s.markUnsupported(ctx, sourceKey, target)
		return false
	case nativeMissing:
		s.recordMissingTranscript(ctx, sourceKey, target, plan.previous)
		return false
	case nativeFailed:
		s.recordSourceError(ctx, sourceKey, domain.SessionSearchSourceNative, target, plan.err)
		return false
	}
	if plan.reset {
		if err := s.store.DeleteSessionSearchDocsForSource(ctx, sourceKey); err != nil {
			s.log.Warn("session search reset failed", "source", sourceKey, "error", err)
			return false
		}
	}
	if plan.upToDate {
		return s.writeTitleDoc(ctx, sourceKey, target)
	}
	if _, err := s.writeChanged(ctx, sourceKey, plan.docs, false); err != nil {
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
		Path:        plan.path,
		CursorValue: plan.nextOffset,
		FileSize:    plan.info.Size(),
		FileMtime:   plan.info.ModTime().UnixNano(),
		Fingerprint: plan.format,
		DocCount:    count,
		State:       domain.SessionSearchSourceActive,
		UpdatedAt:   plan.now,
	}
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		s.log.Warn("session search source update failed", "source", sourceKey, "error", err)
		return false
	}
	return len(plan.docs) > 0
}

const (
	nativePrepareWorkers = 3
	nativePrepareAhead   = 6
)

func (s *Service) forEachNativePlan(ctx context.Context, targets []domain.SessionSearchTarget, apply func(nativePlan) bool) {
	if len(targets) == 0 {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	var wg sync.WaitGroup
	defer func() {
		cancel()
		wg.Wait()
	}()
	plans := make([]chan nativePlan, len(targets))
	for index := range plans {
		plans[index] = make(chan nativePlan, 1)
	}
	ahead := make(chan struct{}, nativePrepareAhead)
	work := make(chan int)
	wg.Add(1)
	go func() {
		defer wg.Done()
		defer close(work)
		for index := range targets {
			select {
			case ahead <- struct{}{}:
			case <-ctx.Done():
				return
			}
			select {
			case work <- index:
			case <-ctx.Done():
				return
			}
		}
	}()
	for range min(nativePrepareWorkers, len(targets)) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for index := range work {
				plans[index] <- s.planNativeSessionSafely(ctx, targets[index])
			}
		}()
	}
	for index := range targets {
		var plan nativePlan
		select {
		case plan = <-plans[index]:
		case <-ctx.Done():
			return
		}
		<-ahead
		if !apply(plan) {
			return
		}
	}
}

func (s *Service) markUnsupported(ctx context.Context, sourceKey string, target domain.SessionSearchTarget) {
	previous, had, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		return
	}
	if !s.writeTitleDoc(ctx, sourceKey, target) && had && previous.State == domain.SessionSearchSourceUnsupported {
		return
	}
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

func (s *Service) recordMissingTranscript(ctx context.Context, sourceKey string, target domain.SessionSearchTarget, previous domain.SessionSearchSource) {
	source := previous
	source.SourceKey = sourceKey
	source.Kind = domain.SessionSearchSourceNative
	source.SessionID = string(target.ID)
	source.ProjectID = string(target.ProjectID)
	source.Harness = string(target.Harness)
	source.State = domain.SessionSearchSourceNoTranscript
	source.Fingerprint = nativeIdentity(target)
	source.UpdatedAt = s.now()
	if err := s.store.UpsertSessionSearchSource(ctx, source); err != nil {
		s.log.Warn("session search source update failed", "source", sourceKey, "error", err)
	}
}

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

func (s *Service) locateTranscript(target domain.SessionSearchTarget) (string, error) {
	if target.Harness != domain.HarnessClaudeCode && target.Harness != domain.HarnessCodex {
		return "", nil
	}
	if path, ok := s.recordedTranscript(target); ok {
		return path, nil
	}
	nativeID := strings.TrimSpace(target.NativeSessionID)
	if nativeID == "" {
		nativeID = string(target.ID)
	}
	switch target.Harness {
	case domain.HarnessClaudeCode:
		return s.locateClaudeTranscript(nativeID, target.WorkspacePath)
	case domain.HarnessCodex:
		return s.locateCodexTranscript(nativeID)
	case domain.HarnessOpenCode, domain.HarnessOpenCodeV2:
		return "", nil
	default:
		return "", nil
	}
}

func transcriptNamesSession(path string, target domain.SessionSearchTarget) bool {
	nativeID := strings.TrimSpace(target.NativeSessionID)
	return nativeID == "" || strings.Contains(filepath.Base(path), nativeID)
}

func (s *Service) recordedTranscript(target domain.SessionSearchTarget) (string, bool) {
	recorded := strings.TrimSpace(target.NativeTranscriptPath)
	if recorded == "" || !filepath.IsAbs(recorded) {
		return "", false
	}
	roots := append(append([]string{}, s.transcriptRootsFor(target.Harness)...), s.roots.AOStateDir)
	for _, root := range roots {
		if strings.TrimSpace(root) == "" {
			continue
		}
		safe, ok := withinRoot(root, recorded)
		if !ok || filepath.Ext(safe) != ".jsonl" {
			continue
		}
		if info, err := os.Stat(safe); err == nil && info.Mode().IsRegular() {
			return safe, true
		}
	}
	return "", false
}

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

type nativeParser func(line []byte) []nativeLine

func nativeParserFor(harness domain.AgentHarness, path, known string, size int64) (nativeParser, string) {
	switch harness {
	case domain.HarnessClaudeCode:
		return parseClaudeTranscriptLine, nativeFormat
	case domain.HarnessCodex:
		if known == nativeFormat+"+items" || (known != nativeFormat && codexHasItemStream(path, size)) {
			return codexParser(true), nativeFormat + "+items"
		}
		return codexParser(false), nativeFormat
	default:
		return nil, ""
	}
}

const nativeMissRecheck = 5 * time.Minute

func regularFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

func nativeIdentity(target domain.SessionSearchTarget) string {
	return "identity:" + target.NativeSessionID + "\x00" + target.NativeTranscriptPath + "\x00" + target.WorkspacePath
}

func nativeHarnessSupported(harness domain.AgentHarness) bool {
	return harness == domain.HarnessClaudeCode || harness == domain.HarnessCodex
}

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
		Type    string          `json:"type"`
		Text    string          `json:"text"`
		Name    string          `json:"name"`
		Input   json.RawMessage `json:"input"`
		Content json.RawMessage `json:"content"`
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
		case "tool_use":
			if body := toolCallText(part.Name, toolArgument(part.Input)); body != "" {
				units = append(units, nativeLine{kind: domain.SessionSearchKindTool, text: body})
			}
		case "tool_result":
			if body := toolOutputText(toolResultText(part.Content)); body != "" {
				units = append(units, nativeLine{kind: domain.SessionSearchKindCommand, text: body})
			}
		}
	}
	return units
}

const codexItemStreamProbeBytes = 256 << 10

func codexHasItemStream(path string, size int64) bool {
	return codexHasItemStreamFrom(path, 0, min(size, codexItemStreamProbeBytes))
}

func codexHasItemStreamFrom(path string, offset, end int64) bool {
	file, err := os.Open(path) //nolint:gosec // provider-owned transcript root, validated by withinRoot
	if err != nil {
		return false
	}
	defer func() { _ = file.Close() }()
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return false
	}
	reader := bufio.NewReaderSize(file, nativeReadChunk)
	var raw []byte
	for consumed := offset; ; {
		raw, err = readLine(reader, raw[:0])
		if err != nil || consumed+int64(len(raw)) > end {
			return false
		}
		consumed += int64(len(raw))
		if codexEventType(raw) == "item_completed" {
			return true
		}
	}
}

func codexEventType(line []byte) string {
	if !bytes.Contains(line, []byte(`"event_msg"`)) {
		return ""
	}
	var record struct {
		Type    string `json:"type"`
		Payload struct {
			Type string `json:"type"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &record) != nil || record.Type != "event_msg" {
		return ""
	}
	return record.Payload.Type
}

func codexParser(itemStream bool) nativeParser {
	return func(line []byte) []nativeLine { return parseCodexTranscriptLine(line, itemStream) }
}

func codexBothStreams(line []byte) []nativeLine {
	var units []nativeLine
	switch codexEventType(line) {
	case "task_started":
		units = append(units, nativeLine{turnStart: true})
	case "item_completed":
		units = append(units, nativeLine{itemRecord: true})
	}
	units = append(units, parseCodexTranscriptLine(line, false)...)
	for _, unit := range parseCodexTranscriptLine(line, true) {
		unit.itemStream = true
		units = append(units, unit)
	}
	return units
}

func keepOneCodexStream(lines []nativeLine) []nativeLine {
	switchAt, turnAt := int64(-1), int64(-1)
	for _, line := range lines {
		if line.turnStart {
			turnAt = line.offset
		}
		if line.itemRecord {
			switchAt = max(turnAt, 0)
			break
		}
	}
	kept := make([]nativeLine, 0, len(lines))
	for _, line := range lines {
		if line.turnStart || line.itemRecord {
			continue
		}
		if line.itemStream == (switchAt >= 0 && line.offset >= switchAt) {
			kept = append(kept, line)
		}
	}
	return kept
}

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

func codexResponseUnits(raw json.RawMessage, at time.Time) []nativeLine {
	var payload struct {
		Type    string `json:"type"`
		Role    string `json:"role"`
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
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
	case "function_call", "custom_tool_call":
		args := payload.Arguments
		if len(args) == 0 {
			args = payload.Input
		}
		if body := toolCallText(payload.Name, toolArgument(args)); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindTool, text: body, at: at}}
		}
	case "function_call_output", "custom_tool_call_output":
		if body := toolOutputText(payload.Output); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindCommand, text: body, at: at}}
		}
	}
	return nil
}

func codexItemUnits(raw json.RawMessage, at time.Time) []nativeLine {
	var payload struct {
		Type string `json:"type"`
		Item struct {
			Type    string `json:"type"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			Command          json.RawMessage            `json:"command"`
			AggregatedOutput string                     `json:"aggregated_output"`
			Output           string                     `json:"output"`
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
		if body := toolText("", codexCommandText(item.Command), output); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindCommand, text: body, at: at}}
		}
	case "FileChange":
		return codexFileChangeUnits(item.Changes, at)
	case "McpToolCall":
		name := item.Tool
		if item.Server != "" {
			name = item.Server + "." + item.Tool
		}
		output := strings.TrimSpace(toolResultText(item.Result) + "\n" + toolResultText(item.Error))
		if body := toolText(name, toolArgument(item.Arguments), output); body != "" {
			return []nativeLine{{kind: domain.SessionSearchKindTool, text: body, at: at}}
		}
	case "Extension":
		argument := item.Query
		if argument == "" {
			argument = toolArgument(item.Action)
		}
		if body := toolText("web_search", argument, rawJSONText(item.Results)); body != "" && argument != "" {
			return []nativeLine{{kind: domain.SessionSearchKindTool, text: body, at: at}}
		}
	}
	return nil
}

func codexCommandText(raw json.RawMessage) string {
	var argv []string
	if json.Unmarshal(raw, &argv) == nil {
		return commandFromArgv(argv)
	}
	var command string
	if json.Unmarshal(raw, &command) == nil {
		return command
	}
	return ""
}

type codexFileChange struct {
	MovePath string `json:"move_path"`
}

func codexFileChangeUnits(changes map[string]codexFileChange, at time.Time) []nativeLine {
	if len(changes) == 0 {
		return nil
	}
	var touched []string
	for path, change := range changes {
		touched = append(touched, path)
		if change.MovePath != "" {
			touched = append(touched, change.MovePath)
		}
	}
	sort.Strings(touched)
	body := toolCallText("apply_patch", strings.Join(touched, "\n"))
	return []nativeLine{{kind: domain.SessionSearchKindPath, text: body, at: at, paths: touched}}
}

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

func readNativeTranscript(ctx context.Context, path string, offset, end int64, parser nativeParser) ([]nativeLine, int64, error) {
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
	var raw []byte
	for {
		if err := ctx.Err(); err != nil {
			return lines, consumed, err
		}
		var readErr error
		raw, readErr = readLine(reader, raw[:0])
		if errors.Is(readErr, io.EOF) {
			return lines, consumed, nil
		}
		if readErr != nil {
			return lines, consumed, readErr
		}
		if consumed+int64(len(raw)) > end {
			return lines, consumed, nil
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

func readLine(reader *bufio.Reader, buf []byte) ([]byte, error) {
	for {
		chunk, err := reader.ReadSlice('\n')
		buf = append(buf, chunk...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return buf, err
		}
	}
}

func boundNativeText(kind domain.SessionSearchKind, text string) string {
	switch kind {
	case domain.SessionSearchKindCommand, domain.SessionSearchKindTool, domain.SessionSearchKindPath:
		return strings.Clone(text)
	}
	if len(text) <= maxDocBodyBytes*maxMessageChunks {
		return text
	}
	return strings.Clone(truncateBytes(text, maxDocBodyBytes*maxMessageChunks))
}
