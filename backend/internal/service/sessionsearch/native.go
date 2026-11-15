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
	// itemStream, turnStart and itemRecord are set only by codexBothStreams:
	// which Codex stream the unit came from, and the markers keepOneCodexStream
	// splits a rollout's turns by. Markers carry no text and are dropped.
	itemStream, turnStart, itemRecord bool
}

// nativeFormat is the version of what the native readers extract. It is
// recorded on each native source; a source read by an older version is reset
// and read again from the start, because a native cursor is a byte offset and
// would otherwise never revisit text an older reader skipped. Bump it when
// the documents a transcript produces change.
//
// v6: a Codex rollout keeps the turns written before its item stream began, and
// an item record is recognised by its type; sources an earlier reader switched
// to the item stream on a message's text are read again.
const nativeFormat = "native-v6"

// nativeDocs turns one parsed unit into its documents. A tool row is already
// within its caps (coverage.go) and stays one document; a long message is
// split into chunks.
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

// nativeOutcome is what preparing a native source decided to do with it.
type nativeOutcome int

const (
	nativeSkip        nativeOutcome = iota // nothing to write (a read failed and was logged, or nothing new)
	nativeOpenCode                         // read through the OpenCode database instead
	nativeUnsupported                      // the harness has no transcript AO reads
	nativeMissing                          // no transcript on disk
	nativeFailed                           // record err on the source
	nativeRead                             // docs are ready to write
)

// nativePlan is everything indexing one native source needs, gathered by
// planNativeSession without writing anything, so plans can be prepared in
// parallel (forEachNativePlan) while one goroutine writes them in order.
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

// planNativeSessionSafely plans one source, turning a panic while parsing its
// provider-owned file into that source's failure: one malformed transcript
// must not stop the daemon, and the index is derived data that can wait.
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

// planNativeSession reads a source's state and its transcript and builds its
// documents. It only reads.
//
// A transcript is opened read-only and read forward from the stored byte
// offset. A file whose identity or size went backwards was rewritten or
// truncated, so the source restarts from zero instead of reading from a stale
// offset.
func (s *Service) planNativeSession(ctx context.Context, target domain.SessionSearchTarget) nativePlan {
	plan := nativePlan{target: target, sourceKey: NativeSourceKey(target.Harness, string(target.ID))}
	// OpenCode keeps its history in its own database rather than a transcript,
	// so it is read through its own path.
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
	// Re-globbing a provider's whole tree every sweep for a session with no
	// transcript is the most expensive thing an idle sweep can do, so a recent
	// miss is trusted: unless the provider reported something new, a transcript
	// path is known and exists again (a file restored in place), or the miss is
	// old enough to look again for one created since.
	if hadPrevious && previous.State == domain.SessionSearchSourceNoTranscript &&
		previous.Fingerprint == nativeIdentity(target) && s.now().Sub(previous.UpdatedAt) < nativeMissRecheck {
		_, reported := s.recordedTranscript(target)
		if !reported && !regularFile(previous.Path) {
			plan.outcome = nativeSkip
			return plan
		}
	}
	// Reuse the recorded path when it still exists. Resolving from scratch globs
	// the provider's whole transcript tree, and doing that per session on every
	// 30-second sweep is the single most expensive thing an idle sweep can do.
	path := ""
	// The transcript the provider last reported is the session's current one,
	// even when an older one is still on disk: a continuation keeps the AO
	// session id but starts a new native session and transcript.
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
	// A source read by an older reader, or a Codex rollout that started writing
	// its item stream after it was first read, is read again from the start.
	// The format the source was last read with stands while the rollout is the
	// same file and has not grown, or once its item stream was seen.
	known := ""
	if hadPrevious && previous.Path == path && (previous.FileSize == info.Size() || previous.Fingerprint == nativeFormat+"+items") {
		known = previous.Fingerprint
	}
	// The probes and the read below see the file as it was when it was stat'ed:
	// a record finished since is neither probed nor read until the next pass.
	parser, format := nativeParserFor(target.Harness, path, known, info.Size())
	plan.format = format
	formatChanged := hadPrevious && previous.Fingerprint != format
	offset := int64(0)
	if hadPrevious && !formatChanged && previous.Path == path && previous.FileSize <= info.Size() {
		offset = previous.CursorValue
	}
	// A legacy rollout can start its item stream past the head the probe reads.
	// The legacy parser skips item records, so the part about to be read is
	// checked first: reading it would move the cursor past records it cannot
	// parse. Finding one switches the source to the item stream from the start.
	if target.Harness == domain.HarnessCodex && format == nativeFormat && offset < info.Size() && codexHasItemStreamFrom(path, offset, info.Size()) {
		parser, format = codexParser(true), nativeFormat+"+items"
		plan.format = format
		formatChanged = hadPrevious && previous.Fingerprint != format
		offset = 0
	}
	// An item-stream rollout read from its start may have turns written before
	// its item stream began, which only the response_items hold: both streams
	// are read and each turn is kept from one of them (keepOneCodexStream).
	bothStreams := target.Harness == domain.HarnessCodex && format == nativeFormat+"+items" && offset == 0
	if bothStreams {
		parser = codexBothStreams
	}
	// The transcript was replaced or truncated, or an older reader indexed
	// it: its earlier documents describe text this pass will not rewrite.
	plan.reset = hadPrevious && (formatChanged || previous.Path != path || previous.FileSize > info.Size())
	if offset >= info.Size() && hadPrevious && previous.State == domain.SessionSearchSourceActive {
		// Nothing new to read, but the session may have been renamed.
		plan.upToDate = true
		plan.outcome = nativeRead
		return plan
	}
	// Read only what was there when the format was decided: a record appended
	// since (the first item record, say) is left for the next pass, which sees
	// the file grew and checks it, rather than skipped by the parser chosen here.
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
	// The session's own name and prompt make it findable even when the provider
	// transcript holds nothing (or the session is a TUI session whose transcript
	// AO could not parse).
	if title, ok := s.sessionTitleDoc(plan.sourceKey, target); ok {
		docs = append(docs, title)
	}
	plan.docs = docs
	plan.outcome = nativeRead
	return plan
}

// applyNativePlan writes what planNativeSession prepared. It returns true when
// it wrote documents.
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
		// No transcript on disk is normal for a Chat session or a session whose
		// workspace is gone. Record that once so later sweeps do not re-glob the
		// provider's whole tree for a session that has nothing to read, then keep
		// any documents already indexed.
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
	// A transcript is read forward from its cursor, so its documents only grow:
	// nothing is deleted here, and unchanged ones (the title) are not rewritten.
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

// Preparing a native source (reading and parsing its transcript, building its
// documents) is most of a backfill's time and needs no lock, so up to
// nativePrepareWorkers sources are prepared at once, at most nativePrepareAhead
// ahead of the one being written. Plans hold a source's documents, which are
// bounded per document (coverage.go), so this also bounds memory.
const (
	nativePrepareWorkers = 3
	nativePrepareAhead   = 6
)

// forEachNativePlan prepares targets in parallel and calls apply with each
// plan in the targets' order, on the calling goroutine. It stops early when
// ctx is done or apply returns false.
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

// markUnsupported records that a harness has no parsed transcript, once, and
// still indexes the session's own name and prompt. "AO cannot read this
// harness's transcript" must not mean "this session cannot be found": the title
// is the one thing AO always knows.
func (s *Service) markUnsupported(ctx context.Context, sourceKey string, target domain.SessionSearchTarget) {
	previous, had, err := s.store.GetSessionSearchSource(ctx, sourceKey)
	if err != nil {
		return
	}
	// The title is the only document such a session has; an unchanged one and an
	// unchanged state cost no write.
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

// recordMissingTranscript remembers that a session has no resolvable transcript.
//
// A Chat session has no native transcript, and a TUI session whose workspace was
// cleaned up may never have one again. Without this record every sweep re-globs
// the provider tree for each such session, which is the most expensive thing an
// idle sweep can do. A recorded miss is trusted for nativeMissRecheck, then the
// tree is searched again and the miss re-recorded, so a transcript created
// later is still found; a known path is checked on every sweep.
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
	if target.Harness != domain.HarnessClaudeCode && target.Harness != domain.HarnessCodex {
		return "", nil
	}
	// A provider names its transcript by its own session id, which AO records,
	// along with the path its hooks report. The path comes first: it also finds
	// a transcript under an AO-managed account's config directory, outside the
	// default roots. A session that never recorded either falls back to its AO
	// id, which is what older sessions were named by.
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
		// OpenCode history lives in its own SQLite store, read by
		// indexOpenCodeSession rather than by a JSONL parser.
		return "", nil
	default:
		return "", nil
	}
}

// transcriptNamesSession reports whether a cached transcript path still belongs
// to the session's current native identity. Claude names the file <id>.jsonl
// and Codex rollout-<time>-<id>.jsonl, so the id appears in the name; a path
// cached for an earlier native session must be resolved again, not reused.
func transcriptNamesSession(path string, target domain.SessionSearchTarget) bool {
	nativeID := strings.TrimSpace(target.NativeSessionID)
	return nativeID == "" || strings.Contains(filepath.Base(path), nativeID)
}

// recordedTranscript is the transcript path the provider's hooks reported, if
// it is still a regular JSONL file inside a provider root or AO's own state
// directory (where an AO-managed account keeps its transcripts). Like every
// other transcript it is only read; the root and the suffix are checked after
// symlinks resolve, so a recorded path cannot lead the reader anywhere else.
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
func nativeParserFor(harness domain.AgentHarness, path, known string, size int64) (nativeParser, string) {
	switch harness {
	case domain.HarnessClaudeCode:
		return parseClaudeTranscriptLine, nativeFormat
	case domain.HarnessCodex:
		// Probing reads the head of the rollout, so a known format is reused.
		if known == nativeFormat+"+items" || (known != nativeFormat && codexHasItemStream(path, size)) {
			return codexParser(true), nativeFormat + "+items"
		}
		return codexParser(false), nativeFormat
	default:
		return nil, ""
	}
}

// nativeMissRecheck is how long a recorded transcript miss is trusted before
// the provider's tree is searched again for one created since.
const nativeMissRecheck = 5 * time.Minute

// regularFile reports whether path names an existing regular file.
func regularFile(path string) bool {
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && info.Mode().IsRegular()
}

// nativeIdentity is what the provider has reported about a session's native
// side. A transcript can only turn up for a recorded miss when it changes.
func nativeIdentity(target domain.SessionSearchTarget) string {
	return "identity:" + target.NativeSessionID + "\x00" + target.NativeTranscriptPath + "\x00" + target.WorkspacePath
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
// build failed. Thinking is not indexed (coverage.go).
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

// codexItemStreamProbeBytes is how much of a rollout codexHasItemStream reads.
// The item stream starts with the first turn, so it is near the top of a file
// that has one.
const codexItemStreamProbeBytes = 256 << 10

// codexHasItemStream reports whether the head of a rollout, within its first
// size bytes, records Codex's item stream (event_msg item_completed), which
// newer Codex writes next to the older response_item stream.
func codexHasItemStream(path string, size int64) bool {
	return codexHasItemStreamFrom(path, 0, min(size, codexItemStreamProbeBytes))
}

// codexHasItemStreamFrom reports whether a rollout holds an item record in
// the complete lines between offset and end. A line that ends past end was
// written after the caller measured the file and is not looked at, as the read
// does not read it. Only a record counts: a message whose text says
// "item_completed" does not.
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
			// The end, a record still being written, or one written since.
			return false
		}
		consumed += int64(len(raw))
		if codexEventType(raw) == "item_completed" {
			return true
		}
	}
}

// codexEventType is an event_msg record's payload type ("item_completed",
// "task_started"), or "" for any other line. A line that does not mention
// event_msg is not decoded.
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

// codexParser reads one Codex rollout.
//
// Newer Codex writes every message and command twice: in the item stream
// (event_msg item_completed, what its UI shows) and as a response_item.
// Reading both would index each one twice. A rollout with an item stream is
// read from that stream only: its user messages are what the person typed
// (the response_item ones also carry injected environment and instructions),
// and it holds the file changes and MCP calls. An older rollout without one
// is read from its response_items.
func codexParser(itemStream bool) nativeParser {
	return func(line []byte) []nativeLine { return parseCodexTranscriptLine(line, itemStream) }
}

// codexBothStreams reads a rollout line from both streams, marking each unit's
// stream, and marks a turn's start (task_started) and any item record.
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

// keepOneCodexStream keeps each turn of a rollout from one stream: the
// response_items for turns before the one in which the item stream starts,
// the item stream from that turn on. Codex writes a turn's response_items
// before its first item record, so switching at the record itself would index
// that turn's messages and commands twice. A rollout with no turn marker
// before its first item record is read from the item stream throughout.
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
		// Web search and fetch: the query is the argument, the result titles
		// the output. Most Extension items carry no text.
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

// codexCommandText renders a CommandExecution's command. Current Codex records
// the argv it ran (`["/bin/zsh", "-lc", "git status"]`); the script after a
// shell's -c/-lc is what the agent wrote, so only that is kept.
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

// codexFileChange is one file in a Codex FileChange item. Only a move's new
// path is read; the diff and content are not indexed.
type codexFileChange struct {
	MovePath string `json:"move_path"`
}

// codexFileChangeUnits indexes a FileChange by the files it touched: they go
// in the paths column and are the row's text. Its diff is not indexed
// (coverage.go).
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

// readNativeTranscript reads complete lines from offset up to end and returns
// the offset after the last line it read. A trailing partial line is left
// unread for the next pass, exactly as the usage parser does: a half-written
// record must not be indexed as if it were final. So is a line that ends past
// end: it was written after the caller measured the file.
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
	// One buffer holds each line in turn. ReadBytes allocated a copy of every
	// line, and a transcript has lines of megabytes of tool output: on 667 MB
	// of transcripts a quarter of the build went to the runtime handing that
	// memory back. The parsers copy whatever they keep.
	var raw []byte
	for {
		if err := ctx.Err(); err != nil {
			return lines, consumed, err
		}
		var readErr error
		raw, readErr = readLine(reader, raw[:0])
		if errors.Is(readErr, io.EOF) {
			// A record without its terminating newline is still being written.
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

// readLine appends the next line, through its newline, to buf.
func readLine(reader *bufio.Reader, buf []byte) ([]byte, error) {
	for {
		chunk, err := reader.ReadSlice('\n')
		buf = append(buf, chunk...)
		if !errors.Is(err, bufio.ErrBufferFull) {
			return buf, err
		}
	}
}

// boundNativeText trims a unit to what its documents will keep as the
// transcript is read, and copies it so the line it came from can be freed. A
// tool row is already within its caps (coverage.go); a message keeps its
// chunks. A transcript of tens of megabytes of tool output then holds a few
// kilobytes per unit, not the file.
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
