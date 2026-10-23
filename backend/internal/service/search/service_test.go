package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ---------- fakes ----------

type fakeWorkspace struct {
	mu   sync.Mutex
	root string
}

func (f *fakeWorkspace) SearchWorkspace(_ context.Context, _ domain.SessionID) (Workspace, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var paths []string
	_ = filepath.WalkDir(f.root, func(p string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			rel, _ := filepath.Rel(f.root, p)
			paths = append(paths, filepath.ToSlash(rel))
		}
		return nil
	})
	sort.Strings(paths)
	return Workspace{Root: f.root, Paths: paths}, nil
}

type fakeMessage struct {
	domain.SearchMessage
	streaming bool
	updated   time.Time
	hidden    bool
}

type fakeMessages struct {
	mu   sync.Mutex
	rows map[string]*fakeMessage
}

func newFakeMessages() *fakeMessages { return &fakeMessages{rows: make(map[string]*fakeMessage)} }

func (f *fakeMessages) put(m fakeMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	cp := m
	f.rows[m.ID] = &cp
}

func (f *fakeMessages) remove(id string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.rows, id)
}

func (f *fakeMessages) SearchMessagesAfter(_ context.Context, at time.Time, id string, limit int) ([]domain.SearchMessageChange, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.SearchMessageChange
	for _, r := range f.rows {
		if r.updated.After(at) || (r.updated.Equal(at) && r.ID > id) {
			out = append(out, domain.SearchMessageChange{ID: r.ID, Text: r.Text, Streaming: r.streaming, UpdatedAt: r.updated})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].UpdatedAt.Equal(out[j].UpdatedAt) {
			return out[i].UpdatedAt.Before(out[j].UpdatedAt)
		}
		return out[i].ID < out[j].ID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMessages) SearchMessageIDsAfter(_ context.Context, id string, limit int) ([]string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for k := range f.rows {
		if k > id {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (f *fakeMessages) SearchMessagesByID(_ context.Context, ids []string) ([]domain.SearchMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []domain.SearchMessage
	for _, id := range ids {
		if r, ok := f.rows[id]; ok {
			m := r.SearchMessage
			m.Streaming = r.streaming
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *fakeMessages) SearchVisibleMessageIDs(_ context.Context, _ string, ids []string) (map[string]bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]bool)
	for _, id := range ids {
		if r, ok := f.rows[id]; ok && !r.hidden {
			out[id] = true
		}
	}
	return out, nil
}

// ---------- fixtures ----------

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) now() time.Time { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	// Distinct modification times so reconcile notices same-size edits.
	later := time.Now().Add(time.Duration(len(content)) * time.Millisecond)
	_ = os.Chtimes(full, later, later)
}

const paletteSource = `package palette

// closePalette hides the palette and keeps the query visible while the
// closing animation plays, so it never flashes an empty search.
func closePalette(state *State) {
	state.open = false
}

func openPalette(state *State) {
	state.open = true
}
`

const configSource = `package config

// resolveDataDir picks where durable state lives.
func resolveDataDir() (string, error) {
	return "", nil
}
`

const callerSource = `package other

func use() {
	_, _ = resolveDataDir()
}
`

func seedRepo(t *testing.T) string {
	root := t.TempDir()
	writeFile(t, root, "palette/palette.go", paletteSource)
	writeFile(t, root, "palette/palette_test.go", "package palette\n\n// closePalette closePalette closePalette in tests\n")
	writeFile(t, root, "config/config.go", configSource)
	writeFile(t, root, "other/use.go", callerSource)
	writeFile(t, root, "docs/notes.md", "Notes about the terminal and keyboard shortcuts.\n")
	writeFile(t, root, "assets/logo.bin", "\x00\x01\x02binary")
	return root
}

func newTestService(t *testing.T, ws WorkspaceSource, msgs MessageStore, c *clock) *Service {
	t.Helper()
	s, err := New(Deps{DataDir: t.TempDir(), Workspaces: ws, Messages: msgs, Now: c.now, SyncInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

var allKinds = Kinds{FileNames: true, FileContent: true, Messages: true}

// searchReady searches until the session's file index has finished its first
// background build.
func searchReady(t *testing.T, s *Service, query string) Response {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		resp, err := s.Search(context.Background(), Request{Query: query, SessionID: "sess-1", Kinds: allKinds})
		if err != nil {
			t.Fatalf("Search(%q): %v", query, err)
		}
		if resp.Files == IndexReady {
			return resp
		}
		if time.Now().After(deadline) {
			t.Fatalf("file index never became ready: %+v", resp.Files)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func contentPaths(r Response) []string {
	var out []string
	for _, h := range r.FileContent {
		out = append(out, h.Path)
	}
	return out
}

// ---------- tests ----------

func TestSearchFilesByNameAndContent(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := newTestService(t, &fakeWorkspace{root: seedRepo(t)}, nil, c)

	first, err := s.Search(context.Background(), Request{Query: "palette", SessionID: "sess-1", Kinds: allKinds})
	if err != nil {
		t.Fatal(err)
	}
	if first.Files != IndexBuilding {
		t.Fatalf("first search state = %v, want building", first.Files)
	}
	if len(first.FileNames) == 0 || first.FileNames[0].Path != "palette/palette.go" {
		t.Fatalf("name hits while building = %+v", first.FileNames)
	}

	vibe := searchReady(t, s, "where do we close the palette")
	if got := contentPaths(vibe); len(got) == 0 || got[0] != "palette/palette.go" {
		t.Fatalf("vibe query content = %v", got)
	}
	if vibe.FileContent[0].Snippet.Line == 0 || len(vibe.FileContent[0].Snippet.Highlights) == 0 {
		t.Fatalf("snippet = %+v", vibe.FileContent[0].Snippet)
	}

	literal := searchReady(t, s, "resolveDataDir")
	if got := contentPaths(literal); len(got) < 2 || got[0] != "config/config.go" {
		t.Fatalf("definition should outrank its call site: %v", got)
	}
	if !literal.FileContent[0].Decisive {
		t.Fatal("literal identifier hit should be decisive")
	}

	typo := searchReady(t, s, typoPalette+" animation")
	if got := contentPaths(typo); len(got) == 0 || got[0] != "palette/palette.go" {
		t.Fatalf("typo query content = %v", got)
	}

	for _, h := range searchReady(t, s, "binary").FileContent {
		if h.Path == "assets/logo.bin" {
			t.Fatal("binary file content was indexed")
		}
	}
}

func TestSearchFilesSeesEditsAndDeletes(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	root := seedRepo(t)
	s := newTestService(t, &fakeWorkspace{root: root}, nil, c)
	searchReady(t, s, "palette")

	writeFile(t, root, "palette/palette.go", paletteSource+"\n// zephyrine marks the new reopen flow\n")
	if err := os.Remove(filepath.Join(root, "config", "config.go")); err != nil {
		t.Fatal(err)
	}
	c.advance(fileStaleAfter + time.Second)
	var got Response
	deadline := time.Now().Add(10 * time.Second)
	for {
		got = searchReady(t, s, "zephyrine")
		if len(got.FileContent) > 0 || time.Now().After(deadline) {
			break
		}
		c.advance(fileStaleAfter + time.Second)
		time.Sleep(20 * time.Millisecond)
	}
	if paths := contentPaths(got); len(paths) == 0 || paths[0] != "palette/palette.go" {
		t.Fatalf("edit not indexed: %v", paths)
	}
	c.advance(fileStaleAfter + time.Second)
	for _, h := range searchReady(t, s, "resolveDataDir").FileContent {
		if h.Path == "config/config.go" {
			t.Fatal("deleted file still returned")
		}
	}
}

func TestFileIndexSurvivesReopen(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	dataDir := t.TempDir()
	ws := &fakeWorkspace{root: seedRepo(t)}
	s, err := New(Deps{DataDir: dataDir, Workspaces: ws, Now: c.now, SyncInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	searchReady(t, s, "palette")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	again, err := New(Deps{DataDir: dataDir, Workspaces: ws, Now: c.now, SyncInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = again.Close() }()
	resp, err := again.Search(context.Background(), Request{Query: "closePalette", SessionID: "sess-1", Kinds: allKinds})
	if err != nil {
		t.Fatal(err)
	}
	if resp.Files != IndexReady || len(resp.FileContent) == 0 {
		t.Fatalf("reopened index not ready: %+v", resp)
	}
}

func syncMessages(t *testing.T, s *Service) {
	t.Helper()
	if err := s.messages.syncOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func messageIDs(r Response) []string {
	var out []string
	for _, h := range r.Messages {
		out = append(out, h.MessageID)
	}
	return out
}

func TestSearchChatMessages(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	msgs := newFakeMessages()
	base := c.now()
	msg := func(id, text string, at time.Duration) fakeMessage {
		return fakeMessage{
			SearchMessage: domain.SearchMessage{ID: id, ConversationID: "conv-1", Text: text,
				SessionID: "sess-1", ConversationTitle: "Palette work", CreatedAt: base.Add(at)},
			updated: base.Add(at),
		}
	}
	msgs.put(msg("m1", "We should close the palette after the animation finishes.", 0))
	msgs.put(msg("m2", "The sidebar toggle lives in the shell.", time.Second))
	streaming := msg("m3", "Half a sentence about kangaroo", 2*time.Second)
	streaming.streaming = true
	msgs.put(streaming)
	s := newTestService(t, nil, msgs, c)
	syncMessages(t, s)

	search := func(q string) Response {
		t.Helper()
		resp, err := s.Search(context.Background(), Request{Query: q, Kinds: Kinds{Messages: true}})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	resp := search("close the palette")
	if len(resp.Messages) == 0 || resp.Messages[0].MessageID != "m1" {
		t.Fatalf("phrase search = %+v", resp)
	}
	hit := resp.Messages[0]
	if hit.ConversationTitle != "Palette work" || hit.SessionID != "sess-1" || len(hit.Snippet.Highlights) == 0 {
		t.Fatalf("hit = %+v", hit)
	}
	if ids := messageIDs(search("palete animaton")); len(ids) == 0 || ids[0] != "m1" {
		t.Fatalf("typo search = %v", ids)
	}
	if ids := messageIDs(search("kangaroo")); len(ids) != 0 {
		t.Fatalf("streaming message searchable: %v", ids)
	}

	// The reply settles: now searchable.
	settled := streaming
	settled.streaming = false
	settled.Text = "A full sentence about kangaroo migrations."
	settled.updated = base.Add(3 * time.Second)
	msgs.put(settled)
	syncMessages(t, s)
	if ids := messageIDs(search("kangaroo migrations")); len(ids) == 0 || ids[0] != "m3" {
		t.Fatalf("settled message = %v", ids)
	}

	// An edit replaces the old text.
	edited := msg("m2", "The inspector toggle lives in the shell.", 4*time.Second)
	msgs.put(edited)
	syncMessages(t, s)
	if ids := messageIDs(search("sidebar")); len(ids) != 0 {
		t.Fatalf("removed word still matches: %v", ids)
	}
	if ids := messageIDs(search("inspector toggle")); len(ids) == 0 || ids[0] != "m2" {
		t.Fatalf("edited text not searchable: %v", ids)
	}

	// A message forked away by an edit is labeled and demoted.
	hidden := msg("m1", "We should close the palette after the animation finishes.", 0)
	hidden.hidden = true
	msgs.put(hidden)
	resp = search("close the palette animation")
	if len(resp.Messages) == 0 || !resp.Messages[0].OlderVersion || resp.Messages[0].Decisive {
		t.Fatalf("forked-away message = %+v", resp.Messages)
	}

	// Deleted from the store: dropped lazily on the next search.
	msgs.remove("m3")
	if ids := messageIDs(search("kangaroo")); len(ids) != 0 {
		t.Fatalf("deleted message returned: %v", ids)
	}
	if _, ok := s.messages.chunks["m3"]; ok {
		t.Fatal("deleted message kept in the index manifest")
	}
}

func TestSearchRejectsBadQueries(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	s := newTestService(t, nil, newFakeMessages(), c)
	if _, err := s.Search(context.Background(), Request{Query: "   "}); err == nil {
		t.Fatal("empty query accepted")
	}
	if _, err := s.Search(context.Background(), Request{Query: strings.Repeat("a", maxQueryLen+1)}); err == nil {
		t.Fatal("over-long query accepted")
	}
}

func TestIndexDirNameIsPathSafe(t *testing.T) {
	if got := indexDirName("sess-1_a.b"); got != "sess-1_a.b" {
		t.Fatalf("safe id rewritten: %q", got)
	}
	for _, bad := range []string{"../escape", "a/b", "", ".hidden"} {
		got := indexDirName(bad)
		if filepath.Base(got) != got || got == "." || got == ".." || got[0] == '.' {
			t.Fatalf("indexDirName(%q) = %q", bad, got)
		}
	}
}

func TestSafeJoinRefusesEscapes(t *testing.T) {
	root := t.TempDir()
	if _, ok := safeJoin(root, "../etc/passwd"); ok {
		t.Fatal("parent escape allowed")
	}
	if _, ok := safeJoin(root, "/etc/passwd"); ok {
		t.Fatal("absolute path allowed")
	}
	if p, ok := safeJoin(root, "a/b.go"); !ok || p != filepath.Join(root, "a", "b.go") {
		t.Fatalf("safeJoin(a/b.go) = %q %v", p, ok)
	}
}

func TestChatPhraseFindsMessageAmongManySharingItsWords(t *testing.T) {
	c := &clock{t: time.Unix(1_800_000_000, 0)}
	msgs := newFakeMessages()
	base := c.now()
	for i := 0; i < 80; i++ {
		msgs.put(fakeMessage{SearchMessage: domain.SearchMessage{
			ID: fmt.Sprintf("noise-%02d", i), ConversationID: "conv",
			Text: "deploy deploy the deploy script again " + strings.Repeat("x", i%5), CreatedAt: base,
		}, updated: base.Add(time.Duration(i) * time.Millisecond)})
	}
	msgs.put(fakeMessage{SearchMessage: domain.SearchMessage{
		ID: "target", ConversationID: "conv",
		Text: "ok so we will deploy it after lunch then", CreatedAt: base,
	}, updated: base.Add(time.Second)})
	s := newTestService(t, nil, msgs, c)
	syncMessages(t, s)
	if !s.messages.disk.phrases {
		t.Fatal("chat index has no word positions: phrase queries cannot match")
	}
	// Mostly stop words: the one content word ("deploy") is in every message,
	// so only the exact-phrase pass can surface the remembered sentence.
	resp, err := s.Search(context.Background(), Request{Query: "we will deploy it after", Kinds: Kinds{Messages: true}})
	if err != nil {
		t.Fatal(err)
	}
	if ids := messageIDs(resp); len(ids) == 0 || ids[0] != "target" {
		t.Fatalf("phrase search = %v, want target first", ids)
	}
}
