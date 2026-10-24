// Package search serves the command palette's search over a session's files
// (by name and by content) and over chat messages, without vectors or a
// model: a Bleve keyword index per source, retrieval with typo tolerance and
// synonyms, then one reranker that puts files and chats on the same scale.
//
// Indexes are derived data under <dataDir>/search. Git, the worktree and
// SQLite stay the source of truth; any index can be deleted and is rebuilt.
package search

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// WorkspaceSource resolves which files a session's search covers.
type WorkspaceSource interface {
	SearchWorkspace(ctx context.Context, id domain.SessionID) (Workspace, error)
}

// Deps wires the search service.
type Deps struct {
	// DataDir is the daemon data dir; indexes live under DataDir/search.
	DataDir    string
	Workspaces WorkspaceSource
	Messages   MessageStore
	Logger     *slog.Logger
	Now        func() time.Time
	// SyncInterval overrides how often chat messages are caught up (tests).
	SyncInterval time.Duration
}

// Service owns the file and chat indexes.
type Service struct {
	deps     Deps
	text     *analyzers
	files    *fileIndexes
	messages *messageIndex

	buildSlots chan struct{} // bounds concurrent first builds
	wg         sync.WaitGroup
	cancel     context.CancelFunc
	ctx        context.Context
}

const (
	defaultSyncInterval = 2 * time.Second
	pruneInterval       = 30 * time.Minute
	// fileStaleAfter is how old a file index may be before a search first
	// checks the worktree for edits.
	fileStaleAfter = 2 * time.Second
	// freshnessBudget caps how long a search waits on that check before it
	// answers from the index as it is.
	freshnessBudget = 250 * time.Millisecond
	maxQueryLen     = 512
	// limit caps each group of hits.
	limit = 20
)

// New opens (or creates) the indexes. Call Start to begin indexing chats and
// Close to release everything.
func New(d Deps) (*Service, error) {
	if d.DataDir == "" {
		return nil, errors.New("search: data dir is required")
	}
	if d.Logger == nil {
		d.Logger = slog.Default()
	}
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.SyncInterval <= 0 {
		d.SyncInterval = defaultSyncInterval
	}
	fileMapping, err := newIndexMapping(false)
	if err != nil {
		return nil, err
	}
	chatMapping, err := newIndexMapping(true)
	if err != nil {
		return nil, err
	}
	root := filepath.Join(d.DataDir, "search")
	s := &Service{
		deps:       d,
		text:       newAnalyzers(fileMapping),
		files:      newFileIndexes(filepath.Join(root, "files"), fileMapping),
		buildSlots: make(chan struct{}, 1),
	}
	if d.Messages != nil {
		mi, err := openMessageIndex(filepath.Join(root, "messages"), chatMapping, d.Messages)
		if err != nil {
			return nil, err
		}
		s.messages = mi
	}
	s.ctx, s.cancel = context.WithCancel(context.Background())
	return s, nil
}

// Start runs the chat catch-up loop until Close.
func (s *Service) Start() {
	if s.messages == nil {
		return
	}
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		tick := time.NewTicker(s.deps.SyncInterval)
		defer tick.Stop()
		lastPrune := time.Time{}
		for {
			if err := s.messages.syncOnce(s.ctx); err != nil && s.ctx.Err() == nil {
				s.deps.Logger.Warn("search: chat index catch-up failed", "err", err)
			}
			if s.deps.Now().Sub(lastPrune) >= pruneInterval {
				if err := s.messages.prune(s.ctx); err != nil && s.ctx.Err() == nil {
					s.deps.Logger.Warn("search: chat index prune failed", "err", err)
				}
				lastPrune = s.deps.Now()
			}
			select {
			case <-s.ctx.Done():
				return
			case <-tick.C:
			}
		}
	}()
}

// Close stops background work and closes every index.
func (s *Service) Close() error {
	s.cancel()
	s.wg.Wait()
	var first error
	if err := s.files.closeAll(); err != nil {
		first = err
	}
	if s.messages != nil {
		if err := s.messages.disk.close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// Kinds selects what a search covers.
type Kinds struct {
	FileNames, FileContent, Messages bool
}

// Request is one palette search.
type Request struct {
	Query string
	// SessionID scopes file search to that session's worktree. Empty skips
	// file search.
	SessionID domain.SessionID
	Kinds     Kinds
}

// IndexState describes the file index's readiness, so the palette can say
// "Indexing files…" instead of silently showing fewer results.
type IndexState string

// Index states.
const (
	IndexReady       IndexState = "ready"
	IndexBuilding    IndexState = "building"
	IndexUnavailable IndexState = "unavailable"
)

// FileNameHit is a file whose path matches the query as a name.
type FileNameHit struct {
	Path      string
	Score     float64
	ExactName bool
}

// ContentHit is a passage inside a file.
type ContentHit struct {
	Path     string
	Snippet  Snippet
	Score    float64
	Decisive bool
}

// MessageHit is a chat message.
type MessageHit struct {
	MessageID string
	SessionID domain.SessionID
	ProjectID domain.ProjectID
	// ReviewID names the reviewer chat the message is in, shown inside the
	// reviewed SessionID's view.
	ReviewID          string
	ConversationTitle string
	CreatedAt         time.Time
	Snippet           Snippet
	Score             float64
	Decisive          bool
	// OlderVersion marks a message no longer in the visible transcript (an
	// edit forked past it, or its turn was rolled back).
	OlderVersion bool
}

// Response groups hits by kind. Scores share one scale across kinds.
type Response struct {
	FileNames   []FileNameHit
	FileContent []ContentHit
	Messages    []MessageHit
	Files       IndexState
}

// Search answers one palette query.
func (s *Service) Search(ctx context.Context, req Request) (Response, error) {
	raw := strings.TrimSpace(req.Query)
	if raw == "" {
		return Response{}, apierr.Invalid("SEARCH_QUERY_REQUIRED", "query is required", nil)
	}
	if len(raw) > maxQueryLen {
		return Response{}, apierr.Invalid("SEARCH_QUERY_TOO_LONG", fmt.Sprintf("query is longer than %d bytes", maxQueryLen), nil)
	}
	q := parseQuery(raw)
	resp := Response{Files: IndexUnavailable}

	var (
		ws     Workspace
		fi     *fileIndex
		strong bool
	)
	wantFiles := req.SessionID != "" && (req.Kinds.FileNames || req.Kinds.FileContent) && s.deps.Workspaces != nil
	if wantFiles {
		var (
			release func()
			err     error
		)
		fi, release, err = s.files.acquire(string(req.SessionID))
		if err != nil {
			return Response{}, err
		}
		defer release()
		// A fresh index already knows the file list; only a refresh asks the
		// session for it again.
		ws = fi.snapshot()
		if st := fi.state(); !st.built || ws.Root == "" || s.deps.Now().Sub(st.lastReconcile) >= fileStaleAfter {
			ws, err = s.deps.Workspaces.SearchWorkspace(ctx, req.SessionID)
			if err != nil {
				return Response{}, err
			}
		}
		resp.Files = s.freshen(ctx, fi, ws, req.SessionID)
		if fresh := fi.snapshot(); fresh.Root != "" {
			ws = fresh
		}
		if req.Kinds.FileNames {
			var matches []pathMatch
			matches, strong = matchPaths(raw, ws.Paths, q, limit)
			for _, m := range matches {
				resp.FileNames = append(resp.FileNames, FileNameHit{Path: m.path, Score: m.score, ExactName: m.exactName})
			}
		}
	}
	var disks []*diskIndex
	var vocabs []*vocabulary
	contentOK := fi != nil && req.Kinds.FileContent
	chatsOK := req.Kinds.Messages && s.messages != nil
	if contentOK {
		disks = append(disks, fi.disk)
		vocabs = append(vocabs, fi.disk.vocab)
	}
	if chatsOK {
		disks = append(disks, s.messages.disk)
		vocabs = append(vocabs, s.messages.disk.vocab)
	}
	if len(disks) == 0 {
		return resp, nil
	}
	var correct func(string) string
	if !strong {
		correct = mergeCorrect(vocabs...)
	}
	ts := prepareTerms(q, s.text, combinedIDF(ctx, disks...), correct)
	if len(ts) == 0 {
		return resp, nil
	}
	if contentOK {
		hits, err := s.searchFileContent(ctx, fi, ws, q, ts)
		if err != nil {
			return Response{}, err
		}
		resp.FileContent = hits
	}
	if chatsOK {
		hits, err := s.searchMessages(ctx, q, ts)
		if err != nil {
			return Response{}, err
		}
		resp.Messages = hits
	}
	return resp, nil
}

// freshen keeps a session's file index current. The first build runs in the
// background (one at a time across sessions); after that, a search older than
// fileStaleAfter re-checks the worktree, waiting at most freshnessBudget.
func (s *Service) freshen(ctx context.Context, fi *fileIndex, ws Workspace, session domain.SessionID) IndexState {
	st := fi.state()
	if !st.built {
		s.launch(fi, ws, session, true)
		return IndexBuilding
	}
	if s.deps.Now().Sub(st.lastReconcile) >= fileStaleAfter {
		if done := s.launch(fi, ws, session, false); done != nil {
			select {
			case <-done:
			case <-time.After(freshnessBudget):
			case <-ctx.Done():
			}
		}
	}
	return IndexReady
}

// launch reconciles fi in the background unless that is already queued or
// running. The returned channel closes when the work is done; it is nil when
// nothing was started.
// The work pins the index itself, so it stays open after the search that
// asked for it returns. A first build waits for the shared build slot.
func (s *Service) launch(fi *fileIndex, ws Workspace, session domain.SessionID, first bool) <-chan struct{} {
	if !fi.claimWork() {
		return nil
	}
	pinned, release, err := s.files.acquire(string(session))
	if err != nil || pinned != fi {
		if release != nil {
			release()
		}
		fi.finishWork()
		return nil
	}
	done := make(chan struct{})
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		defer close(done)
		defer release()
		defer fi.finishWork()
		if first {
			select {
			case s.buildSlots <- struct{}{}:
			case <-s.ctx.Done():
				return
			}
			defer func() { <-s.buildSlots }()
		}
		start := s.deps.Now()
		err := fi.reconcile(s.ctx, ws, s.deps.Now)
		switch {
		case err != nil && s.ctx.Err() == nil:
			s.deps.Logger.Warn("search: index files failed", "session", session, "first", first, "err", err)
		case err == nil && first:
			s.deps.Logger.Info("search: file index built", "session", session, "files", len(ws.Paths), "took", s.deps.Now().Sub(start).Round(time.Millisecond))
		}
	}()
	return done
}

const (
	contentCandidates = 60
	messageCandidates = 40
	phraseCandidates  = 20
)

// retrieve runs the word pass plus exact-phrase passes (as typed and as
// corrected) and keeps each document's best normalized score.
func retrieve(ctx context.Context, d *diskIndex, q parsedQuery, ts []queryTerm, size int) ([]rawHit, error) {
	hits, err := d.searchHits(ctx, retrievalQuery(q, ts, d.phrases), size)
	if err != nil {
		return nil, err
	}
	var needles []string
	if !d.phrases {
		return dedupeHits(hits), nil
	}
	if len(strings.Fields(q.needle)) >= 2 {
		needles = append(needles, q.needle)
	}
	if fixed := correctedNeedle(q.needle, ts); fixed != q.needle {
		needles = append(needles, fixed)
	}
	for _, n := range needles {
		more, err := d.searchHits(ctx, phraseQuery(n), phraseCandidates)
		if err != nil {
			return nil, err
		}
		hits = append(hits, more...)
	}
	return dedupeHits(hits), nil
}

// dedupeHits keeps each document's best normalized score, best first.
func dedupeHits(hits []rawHit) []rawHit {
	best := make(map[string]rawHit, len(hits))
	for _, h := range hits {
		if cur, ok := best[h.id]; !ok || h.score > cur.score {
			best[h.id] = h
		}
	}
	return sortedValues(best, len(best), func(a, b rawHit) int {
		return cmp.Or(cmp.Compare(b.score, a.score), strings.Compare(a.id, b.id))
	})
}

// sortedValues returns m's values ordered by order, at most n of them.
func sortedValues[T any](m map[string]T, n int, order func(a, b T) int) []T {
	out := slices.SortedFunc(maps.Values(m), order)
	return out[:min(len(out), n)]
}

func (s *Service) searchFileContent(ctx context.Context, fi *fileIndex, ws Workspace, q parsedQuery, ts []queryTerm) ([]ContentHit, error) {
	hits, err := retrieve(ctx, fi.disk, q, ts, contentCandidates)
	if err != nil {
		return nil, err
	}
	cache := make(map[string][]string)
	files := newWorkspaceFiles(ws.Root)
	bestByPath := make(map[string]ContentHit)
	for _, h := range hits {
		rel, start := splitDocID(h.id)
		text, ok := readChunk(files, rel, start, cache)
		if !ok {
			continue
		}
		f := s.text.features(q, ts, text)
		if f.coverage == 0 && f.phrase == 0 {
			continue // stale chunk: the file changed under the index
		}
		score := contentScore(q, f, h.score)
		pathWeight := weightPathWords
		if q.literal {
			pathWeight *= weightPathLiteral
		}
		score += pathWeight * s.text.features(q, ts, rel).coverage
		if q.literal && definesQueried(text, ts) {
			score += weightDefinition
		}
		score *= filePrior(rel, q)
		if cur, ok := bestByPath[rel]; ok && cur.Score >= score {
			continue
		}
		bestByPath[rel] = ContentHit{
			Path:     rel,
			Snippet:  bestSnippet(text, f.hits, start),
			Score:    score,
			Decisive: decisive(q, ts, f),
		}
	}
	return sortedValues(bestByPath, limit, func(a, b ContentHit) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), strings.Compare(a.Path, b.Path))
	}), nil
}

// olderVersionFactor demotes a message that left the visible transcript.
const olderVersionFactor = 0.5

func (s *Service) searchMessages(ctx context.Context, q parsedQuery, ts []queryTerm) ([]MessageHit, error) {
	hits, err := retrieve(ctx, s.messages.disk, q, ts, messageCandidates)
	if err != nil {
		return nil, err
	}
	if len(hits) == 0 {
		return nil, nil
	}
	ids := make([]string, 0, len(hits))
	seen := make(map[string]bool)
	for _, h := range hits {
		id, _ := splitDocID(h.id)
		if !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	rows, err := s.deps.Messages.SearchMessagesByID(ctx, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[string]domain.SearchMessage, len(rows))
	byConversation := make(map[string][]string)
	for _, r := range rows {
		if r.Streaming {
			continue
		}
		byID[r.ID] = r
		byConversation[r.ConversationID] = append(byConversation[r.ConversationID], r.ID)
	}
	var gone []string
	for _, id := range ids {
		if _, ok := byID[id]; !ok {
			gone = append(gone, id)
		}
	}
	if len(gone) > 0 {
		if err := s.messages.remove(gone); err != nil {
			s.deps.Logger.Warn("search: drop deleted chat messages from index", "err", err)
		}
	}
	visible := make(map[string]bool, len(byID))
	for conversation, cids := range byConversation {
		v, err := s.deps.Messages.SearchVisibleMessageIDs(ctx, conversation, cids)
		if err != nil {
			return nil, err
		}
		for id, ok := range v {
			visible[id] = ok
		}
	}
	best := make(map[string]MessageHit)
	for _, h := range hits {
		id, k := splitDocID(h.id)
		msg, ok := byID[id]
		if !ok {
			continue
		}
		parts := splitMessageChunks(msg.Text)
		if k >= len(parts) {
			continue
		}
		f := s.text.features(q, ts, parts[k])
		if f.coverage == 0 && f.phrase == 0 {
			continue
		}
		score := contentScore(q, f, h.score)
		older := !visible[id]
		if older {
			score *= olderVersionFactor
		}
		if cur, ok := best[id]; ok && cur.Score >= score {
			continue
		}
		best[id] = MessageHit{
			MessageID:         msg.ID,
			SessionID:         msg.SessionID,
			ProjectID:         msg.ProjectID,
			ReviewID:          msg.ReviewID,
			ConversationTitle: msg.ConversationTitle,
			CreatedAt:         msg.CreatedAt,
			Snippet:           bestSnippet(msg.Text, f.hits, 0),
			Score:             score,
			Decisive:          decisive(q, ts, f) && !older,
			OlderVersion:      older,
		}
	}
	return sortedValues(best, limit, func(a, b MessageHit) int {
		return cmp.Or(cmp.Compare(b.Score, a.Score), b.CreatedAt.Compare(a.CreatedAt))
	}), nil
}
