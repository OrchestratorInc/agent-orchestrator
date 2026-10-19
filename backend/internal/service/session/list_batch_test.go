package session

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"sync/atomic"
	"testing"
	"time"

	msqlite "modernc.org/sqlite"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionartifacts"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
	sqlitestore "github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/store"
)

// A list read loads the cleanup facts of every archived session in one query,
// so its cost does not grow with the number of archived sessions.
func TestListLoadsCleanupFactsInOneBatchedQuery(t *testing.T) {
	for _, archived := range []int{1, 7, 60} {
		t.Run(fmt.Sprintf("%d archived", archived), func(t *testing.T) {
			st := newFakeStore()
			st.cleanupFacts = map[domain.SessionID]domain.SessionCleanupRecord{}
			want := map[domain.SessionID]domain.WorkspaceDisposition{}
			var archivedIDs []domain.SessionID
			for i := 0; i < archived; i++ {
				id := domain.SessionID(fmt.Sprintf("mer-%d", i+1))
				st.sessions[id] = domain.SessionRecord{ID: id, ProjectID: "mer", IsTerminated: true, CleanupGeneration: 2}
				archivedIDs = append(archivedIDs, id)
				switch i % 3 {
				case 0: // facts for the current cleanup generation
					st.cleanupFacts[id] = domain.SessionCleanupRecord{SessionID: id, SessionGeneration: 2, WorkspaceDisposition: domain.DispositionRemoved}
					want[id] = domain.DispositionRemoved
				case 1: // facts left by an earlier generation are not current
					st.cleanupFacts[id] = domain.SessionCleanupRecord{SessionID: id, SessionGeneration: 1, WorkspaceDisposition: domain.DispositionFailed}
				}
				// i%3 == 2: the session has no facts row at all.
			}
			st.sessions["mer-live"] = domain.SessionRecord{ID: "mer-live", ProjectID: "mer"}
			st.cleanupFacts["mer-live"] = domain.SessionCleanupRecord{SessionID: "mer-live", WorkspaceDisposition: domain.DispositionRemoved}

			list, err := (&Service{store: st}).List(context.Background(), ListFilter{})
			if err != nil {
				t.Fatal(err)
			}
			if len(list) != archived+1 {
				t.Fatalf("len(list) = %d, want %d", len(list), archived+1)
			}
			if st.getCleanupFactsCalls != 0 {
				t.Fatalf("per-session cleanup facts reads = %d, want 0", st.getCleanupFactsCalls)
			}
			if st.listCleanupFactsCalls != 1 {
				t.Fatalf("batched cleanup facts reads = %d, want 1", st.listCleanupFactsCalls)
			}
			requested := append([]domain.SessionID(nil), st.cleanupFactsRequested...)
			sort.Slice(requested, func(i, j int) bool { return requested[i] < requested[j] })
			sort.Slice(archivedIDs, func(i, j int) bool { return archivedIDs[i] < archivedIDs[j] })
			if fmt.Sprint(requested) != fmt.Sprint(archivedIDs) {
				t.Fatalf("batched read asked for %v, want exactly the archived sessions %v", requested, archivedIDs)
			}
			for _, sess := range list {
				if sess.WorkspaceCleanup != want[sess.ID] {
					t.Fatalf("%s WorkspaceCleanup = %q, want %q", sess.ID, sess.WorkspaceCleanup, want[sess.ID])
				}
			}
		})
	}
}

// Filters that drop every archived session leave nothing to look up.
func TestListSkipsCleanupFactsWhenNoArchivedSessionIsListed(t *testing.T) {
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "mer"}
	st.sessions["mer-2"] = domain.SessionRecord{ID: "mer-2", ProjectID: "mer", IsTerminated: true}
	active := true
	list, err := (&Service{store: st}).List(context.Background(), ListFilter{Active: &active})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != "mer-1" {
		t.Fatalf("list = %+v, want only mer-1", list)
	}
	if st.getCleanupFactsCalls != 0 || st.listCleanupFactsCalls != 0 {
		t.Fatalf("cleanup facts reads = %d single, %d batched, want none", st.getCleanupFactsCalls, st.listCleanupFactsCalls)
	}
}

// listShape is one kind of session the board can hold: live or archived, with
// or without a PR, artifact files, cleanup facts, or a stale output type.
type listShape struct {
	name       string
	terminated bool
	generation int64
	facts      *domain.SessionCleanupRecord // the facts row; SessionID is filled in
	pr         bool
	files      []string // regular files written under the session's artifact dir
	persisted  domain.SessionOutputType
	legacyDir  bool // artifact_dir stored as '' (rows older than the column)
	mustRepair bool // the persisted output type disagrees with the disk
}

func settledShapes() []listShape {
	removed := &domain.SessionCleanupRecord{SessionGeneration: 2, WorkspaceDisposition: domain.DispositionRemoved}
	stale := &domain.SessionCleanupRecord{SessionGeneration: 1, WorkspaceDisposition: domain.DispositionFailed}
	dirty := &domain.SessionCleanupRecord{SessionGeneration: 0, WorkspaceDisposition: domain.DispositionPreservedDirty, AttemptCount: 2, FailureCode: "workspace_dirty"}
	return []listShape{
		{name: "live", persisted: domain.SessionOutputNone},
		{name: "live with PR", pr: true, persisted: domain.SessionOutputPR},
		{name: "live with artifacts", files: []string{"report.html", "notes/plan.md"}, persisted: domain.SessionOutputArtifact},
		{name: "archived with current facts", terminated: true, generation: 2, facts: removed, persisted: domain.SessionOutputNone},
		{name: "archived with stale facts", terminated: true, generation: 2, facts: stale, persisted: domain.SessionOutputNone},
		{name: "archived without facts", terminated: true, persisted: domain.SessionOutputNone},
		{name: "archived with PR and preserved workspace", terminated: true, facts: dirty, pr: true, persisted: domain.SessionOutputPR},
		{name: "archived with PR and artifacts", terminated: true, files: []string{"summary.md"}, pr: true, persisted: domain.SessionOutputPRAndArtifact},
	}
}

func repairShapes() []listShape {
	return []listShape{
		{name: "archived with unreconciled artifacts", terminated: true, files: []string{"late.html"}, persisted: domain.SessionOutputNone, mustRepair: true},
		{name: "live with legacy artifact dir", files: []string{"legacy.md"}, persisted: domain.SessionOutputNone, legacyDir: true, mustRepair: true},
		{name: "live with artifacts gone", persisted: domain.SessionOutputArtifact, mustRepair: true},
	}
}

var listFixtureTime = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func addListSession(t *testing.T, store *sqlitestore.Store, dataDir string, shape listShape, seq int) domain.SessionID {
	t.Helper()
	ctx := context.Background()
	rec, err := store.CreateSession(ctx, domain.SessionRecord{
		ProjectID: "mer", Kind: domain.KindWorker, Harness: domain.HarnessClaudeCode,
		Activity:  domain.Activity{State: domain.ActivityIdle, LastActivityAt: listFixtureTime},
		Metadata:  domain.SessionMetadata{Branch: "feat/x", WorkspacePath: "/ws"},
		CreatedAt: listFixtureTime, UpdatedAt: listFixtureTime,
	})
	if err != nil {
		t.Fatalf("create %s: %v", shape.name, err)
	}
	dir := sessionartifacts.Dir(dataDir, rec.ID)
	for _, name := range shape.files {
		path := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("# "+name), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(path, listFixtureTime, listFixtureTime); err != nil {
			t.Fatal(err)
		}
	}
	if shape.terminated {
		rec.IsTerminated, rec.CleanupGeneration = true, shape.generation
		if err := store.UpdateSession(ctx, rec); err != nil {
			t.Fatalf("terminate %s: %v", shape.name, err)
		}
	}
	storedDir := dir
	if shape.legacyDir {
		storedDir = ""
	}
	if _, err := store.UpdateSessionArtifactOutput(ctx, rec.ID, storedDir, shape.persisted); err != nil {
		t.Fatalf("persist output type of %s: %v", shape.name, err)
	}
	if shape.pr {
		pr := domain.PullRequest{
			URL: fmt.Sprintf("https://github.com/acme/repo/pull/%d", seq), SessionID: rec.ID, Number: seq,
			HeadSHA: "head", UpdatedAt: listFixtureTime, ObservedAt: listFixtureTime,
		}
		if err := store.WriteSCMObservation(ctx, pr, nil, nil, nil, nil, ports.ReviewWritePreserve); err != nil {
			t.Fatalf("add PR to %s: %v", shape.name, err)
		}
	}
	if shape.facts != nil {
		facts := *shape.facts
		facts.SessionID = rec.ID
		if err := store.UpsertSessionCleanupFacts(ctx, facts); err != nil {
			t.Fatalf("add cleanup facts to %s: %v", shape.name, err)
		}
	}
	return rec.ID
}

// countingConnector hands database/sql connections that count the statements
// they run. The counter tallies Query, Exec and Prepare calls; BEGIN and COMMIT
// are not counted.
type countingConnector struct {
	dsn        string
	statements *atomic.Int64
}

func (c countingConnector) Connect(context.Context) (driver.Conn, error) {
	inner, err := (&msqlite.Driver{}).Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &countingConn{Conn: inner, statements: c.statements}, nil
}

func (c countingConnector) Driver() driver.Driver { return &msqlite.Driver{} }

type countingConn struct {
	driver.Conn
	statements *atomic.Int64
}

func (c *countingConn) Prepare(query string) (driver.Stmt, error) {
	c.statements.Add(1)
	return c.Conn.Prepare(query)
}

func (c *countingConn) PrepareContext(ctx context.Context, query string) (driver.Stmt, error) {
	c.statements.Add(1)
	return c.Conn.(driver.ConnPrepareContext).PrepareContext(ctx, query)
}

func (c *countingConn) ExecContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Result, error) {
	c.statements.Add(1)
	return c.Conn.(driver.ExecerContext).ExecContext(ctx, query, args)
}

func (c *countingConn) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	c.statements.Add(1)
	return c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
}

func (c *countingConn) CheckNamedValue(nv *driver.NamedValue) error {
	if checker, ok := c.Conn.(driver.NamedValueChecker); ok {
		return checker.CheckNamedValue(nv)
	}
	return driver.ErrSkip
}

// openCountingStore opens a migrated SQLite store holding project "mer", whose
// connections count their statements, with the same writer and reader pool
// shape as sqlite.Open.
func openCountingStore(t *testing.T, statements *atomic.Int64) (store *sqlitestore.Store, dataDir string) {
	t.Helper()
	dataDir = t.TempDir()
	seed, err := sqlitetest.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := seed.Close(); err != nil {
		t.Fatal(err)
	}
	file := url.URL{Path: filepath.Join(dataDir, "ao.db")}
	dsn := "file:" + file.EscapedPath() +
		"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)"
	open := func(conns int) *sql.DB {
		db := sql.OpenDB(countingConnector{dsn: dsn, statements: statements})
		db.SetMaxOpenConns(conns)
		db.SetMaxIdleConns(conns)
		return db
	}
	store = sqlitestore.NewStore(open(1), open(8))
	t.Cleanup(func() { _ = store.Close() })
	if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "mer", Path: "/tmp/mer", RegisteredAt: listFixtureTime}); err != nil {
		t.Fatal(err)
	}
	return store, dataDir
}

func newListFixtureService(store *sqlitestore.Store, dataDir string, reconciler outputTypeReconciler) *Service {
	return NewWithDeps(Deps{
		Store: store, DataDir: dataDir, OutputTypeReconciler: reconciler,
		Clock: func() time.Time { return listFixtureTime.Add(time.Hour) },
	})
}

// listFingerprint serializes everything a list or get response is built from.
func listFingerprint(t *testing.T, sess domain.Session) string {
	t.Helper()
	out, err := json.Marshal(struct {
		domain.Session
		Metadata      domain.SessionMetadata
		ArtifactDir   string
		ArtifactFiles []domain.SessionArtifactFile
		PRs           []domain.PRFacts
		AgentSwitch   *domain.AgentSwitch
	}{sess, sess.Metadata, sess.Metadata.ArtifactDir, sess.ArtifactFiles, sess.PRs, sess.ActiveAgentSwitch})
	if err != nil {
		t.Fatal(err)
	}
	return string(out)
}

// Listing returns, for every kind of session, exactly what reading that session
// on its own returns: the per-session read is the path the list used before it
// batched, and it still reads cleanup facts one session at a time.
func TestListMatchesGetForEverySessionShape(t *testing.T) {
	var statements atomic.Int64
	store, dataDir := openCountingStore(t, &statements)
	reconciler := &fakeOutputTypeReconciler{}
	svc := newListFixtureService(store, dataDir, reconciler)

	names := map[domain.SessionID]string{}
	repair := map[domain.SessionID]bool{}
	seq := 0
	for _, shape := range append(settledShapes(), repairShapes()...) {
		seq++
		id := addListSession(t, store, dataDir, shape, seq)
		names[id] = shape.name
		repair[id] = shape.mustRepair
	}

	list, err := svc.List(context.Background(), ListFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(names) {
		t.Fatalf("listed %d sessions, want %d", len(list), len(names))
	}
	listReconciled := append([]domain.SessionID(nil), reconciler.reconciled...)
	reconciler.reconciled = nil
	for _, sess := range list {
		got, err := svc.Get(context.Background(), sess.ID)
		if err != nil {
			t.Fatalf("get %s: %v", names[sess.ID], err)
		}
		if a, b := listFingerprint(t, sess), listFingerprint(t, got); a != b {
			t.Errorf("%s: list and get disagree\nlist: %s\nget:  %s", names[sess.ID], a, b)
		}
	}
	// The self-repair fires for exactly the sessions whose persisted output type
	// is wrong, on a list read as on a get.
	sort.Slice(listReconciled, func(i, j int) bool { return listReconciled[i] < listReconciled[j] })
	sort.Slice(reconciler.reconciled, func(i, j int) bool { return reconciler.reconciled[i] < reconciler.reconciled[j] })
	var wantRepair []domain.SessionID
	for id, must := range repair {
		if must {
			wantRepair = append(wantRepair, id)
		}
	}
	sort.Slice(wantRepair, func(i, j int) bool { return wantRepair[i] < wantRepair[j] })
	if fmt.Sprint(listReconciled) != fmt.Sprint(wantRepair) {
		t.Errorf("list repaired %v, want %v", listReconciled, wantRepair)
	}
	if fmt.Sprint(reconciler.reconciled) != fmt.Sprint(wantRepair) {
		t.Errorf("get repaired %v, want %v", reconciler.reconciled, wantRepair)
	}

	byName := map[string]domain.Session{}
	for _, sess := range list {
		byName[names[sess.ID]] = sess
	}
	for name, want := range map[string]domain.WorkspaceDisposition{
		"archived with current facts":              domain.DispositionRemoved,
		"archived with stale facts":                "",
		"archived without facts":                   "",
		"archived with PR and preserved workspace": domain.DispositionPreservedDirty,
		"live": "",
	} {
		if got := byName[name].WorkspaceCleanup; got != want {
			t.Errorf("%s: WorkspaceCleanup = %q, want %q", name, got, want)
		}
	}
	if got := byName["archived with unreconciled artifacts"]; got.OutputType != domain.SessionOutputArtifact || len(got.ArtifactFiles) != 1 {
		t.Errorf("unreconciled archived session: type %q, files %v, want artifact with late.html", got.OutputType, got.ArtifactFiles)
	}
	if got := byName["live with legacy artifact dir"]; got.Metadata.ArtifactDir != sessionartifacts.Dir(dataDir, got.ID) || got.OutputType != domain.SessionOutputArtifact {
		t.Errorf("legacy session: artifact dir %q, type %q, want the derived dir and artifact", got.Metadata.ArtifactDir, got.OutputType)
	}
	if got := byName["live with artifacts gone"]; got.OutputType.HasArtifact() || len(got.ArtifactFiles) != 0 {
		t.Errorf("session whose artifacts are gone: type %q, files %v, want none", got.OutputType, got.ArtifactFiles)
	}
	if got := byName["live with artifacts"]; len(got.ArtifactFiles) != 2 || got.ArtifactFiles[1].Path != "report.html" {
		t.Errorf("live session artifacts = %+v, want notes/plan.md and report.html", got.ArtifactFiles)
	}
}

// The number of SQL statements behind a list read does not depend on how many
// sessions it lists. Sessions whose output type needs a one-time repair are
// left out on purpose: each repair is its own reconcile.
func TestListIssuesTheSameStatementsForAnySessionCount(t *testing.T) {
	measure := func(groups int) int64 {
		var statements atomic.Int64
		store, dataDir := openCountingStore(t, &statements)
		svc := newListFixtureService(store, dataDir, &fakeOutputTypeReconciler{})
		seq := 0
		for g := 0; g < groups; g++ {
			for _, shape := range settledShapes() {
				seq++
				addListSession(t, store, dataDir, shape, seq)
			}
		}
		statements.Store(0)
		list, err := svc.List(context.Background(), ListFilter{})
		if err != nil {
			t.Fatal(err)
		}
		if want := groups * len(settledShapes()); len(list) != want {
			t.Fatalf("listed %d sessions, want %d", len(list), want)
		}
		return statements.Load()
	}
	small, large := measure(1), measure(8)
	if small != large {
		t.Fatalf("a list of %d sessions runs %d statements and a list of %d runs %d; want the same number",
			len(settledShapes()), small, 8*len(settledShapes()), large)
	}
	if large > 8 {
		t.Fatalf("a list read runs %d statements, want at most 8", large)
	}
	t.Logf("a list read runs %d SQL statements for %d and for %d sessions", large, len(settledShapes()), 8*len(settledShapes()))
}
