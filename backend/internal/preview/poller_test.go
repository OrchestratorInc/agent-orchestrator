package preview

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

type fakePreviewSessions struct {
	sessions []domain.SessionRecord
	sets     []previewSet
}

type previewSet struct {
	id  domain.SessionID
	url string
}

func (f *fakePreviewSessions) ListActiveWorkerPreviews(_ context.Context) ([]domain.SessionPreviewRecord, error) {
	var out []domain.SessionPreviewRecord
	for _, sess := range f.sessions {
		if sess.IsTerminated || sess.Kind != domain.KindWorker {
			continue
		}
		out = append(out, domain.SessionPreviewRecord{
			ID:              sess.ID,
			WorkspacePath:   sess.Metadata.WorkspacePath,
			PreviewURL:      sess.Metadata.PreviewURL,
			PreviewRevision: sess.Metadata.PreviewRevision,
		})
	}
	return out, nil
}

func (f *fakePreviewSessions) SetPreview(_ context.Context, id domain.SessionID, previewURL string) (domain.Session, error) {
	f.sets = append(f.sets, previewSet{id: id, url: previewURL})
	for i, sess := range f.sessions {
		if sess.ID == id {
			sess.Metadata.PreviewURL = previewURL
			f.sessions[i] = sess
			return domain.Session{SessionRecord: sess}, nil
		}
	}
	return domain.Session{}, nil
}

func TestPollerDoesNotOpenUntargetedWorkerEntry(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets)
}

func TestPollerNormalizesExplicitWorkspaceEntrypoint(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "dist", "index.html"), "<main>dist</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "dist/index.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: mustFileURL(t, "http://127.0.0.1:3001", "ao-1", "dist/index.html"),
	})
}

func TestPollerDoesNotReplaceExplicitEntrypointWithAnotherStaticFile(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "public", "index.html"), "<main>public</main>")
	writeFile(t, filepath.Join(workspace, "dist", "index.html"), "<main>dist</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "dist/index.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: mustFileURL(t, "http://127.0.0.1:3001", "ao-1", "dist/index.html"),
	})
}

func TestPollerRefreshesOnlyWhenEntrypointChanges(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "index.html")
	writeFile(t, entry, "<main>v1</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "index.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("second Poll: %v", err)
	}
	if len(svc.sets) != 1 {
		t.Fatalf("sets after unchanged entry = %#v, want one set", svc.sets)
	}

	writeFile(t, entry, "<main>v2 changed</main>")
	nextMod := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(entry, nextMod, nextMod); err != nil {
		t.Fatalf("chtimes entry: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("third Poll: %v", err)
	}

	if len(svc.sets) != 2 {
		t.Fatalf("sets after changed entry = %#v, want refresh set", svc.sets)
	}
}

func TestPollerRediscoverEntryAfterDeleteAndRecreate(t *testing.T) {
	workspace := t.TempDir()
	entry := filepath.Join(workspace, "index.html")
	writeFile(t, entry, "<main>v1</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "index.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	// First poll normalizes the explicitly selected workspace entry.
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("first Poll: %v", err)
	}
	wantURL := mustFileURL(t, "http://127.0.0.1:3001", "ao-1", "index.html")
	assertSets(t, svc.sets, previewSet{id: "ao-1", url: wantURL})

	// Delete the entry — poller must clear the preview and mark the session cleared.
	if err := os.Remove(entry); err != nil {
		t.Fatalf("remove index.html: %v", err)
	}
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("second Poll (delete): %v", err)
	}
	if len(svc.sets) != 2 {
		t.Fatalf("sets after delete = %#v, want clear + set", svc.sets)
	}
	if svc.sets[1].url != "" {
		t.Fatalf("second set.url = %q, want empty (clear)", svc.sets[1].url)
	}

	// Recreate the entry — poller must re-discover.
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("third Poll (still deleted): %v", err)
	}
	if len(svc.sets) != 2 {
		t.Fatalf("sets while entry remains deleted = %#v, want no repeated clear", svc.sets)
	}

	writeFile(t, entry, "<main>v2</main>")
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("fourth Poll (recreate): %v", err)
	}
	if len(svc.sets) != 3 {
		t.Fatalf("sets after recreate = %#v, want 3 sets (discover + clear + rediscover)", svc.sets)
	}
	if svc.sets[2].url != wantURL {
		t.Fatalf("third set.url = %q, want %q", svc.sets[2].url, wantURL)
	}
}

func TestPollerDoesNotRestoreClearedPreviewAfterRestart(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	sess := workerSession("ao-1", workspace, "")
	sess.Metadata.PreviewRevision = 2
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{sess}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want cleared preview to remain empty after restart", svc.sets)
	}
}

func TestPollerDoesNotOverrideExplicitPreviewTarget(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "file:///C:/tmp/other.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no automatic override", svc.sets)
	}
}

func TestPollerMigratesLegacyWorkspacePreviewURL(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	writeFile(t, filepath.Join(workspace, "docs", "report.html"), "<main>chosen report</main>")
	legacy := "http://127.0.0.1:3001/api/v1/sessions/ao-1/preview/files/docs/report.html"
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, legacy)}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: mustFileURL(t, "http://127.0.0.1:3001", "ao-1", "docs/report.html"),
	})
}

func TestPollerPreservesStoredRelativeEntry(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>default</main>")
	writeFile(t, filepath.Join(workspace, "docs", "report.html"), "<main>chosen report</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "docs/report.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: mustFileURL(t, "http://127.0.0.1:3001", "ao-1", "docs/report.html"),
	})
}

func TestPollerRewritesStoredOriginToActualDaemonPortWithoutChangingEntry(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>default</main>")
	writeFile(t, filepath.Join(workspace, "docs", "report.html"), "<main>chosen report</main>")
	old := mustFileURL(t, "http://127.0.0.1:3001", "ao-1", "docs/report.html")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, old)}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:49152", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: mustFileURL(t, "http://127.0.0.1:49152", "ao-1", "docs/report.html"),
	})
}

func TestPollerDoesNotAutoPreviewMarkdownOnlyNewSession(t *testing.T) {
	// A brand-new session (never explicitly previewed) whose workspace contains
	// only Markdown documents — no index.html — must NOT be auto-previewed.
	// Surfacing an arbitrary repo README as the initial browser target is noise.
	// See issue #2859.
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "README.md"), "# project")
	writeFile(t, filepath.Join(workspace, "docs", "setup.md"), "# setup")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no auto-preview for a Markdown-only new session", svc.sets)
	}
}

func TestPollerKeepsMarkdownPreviewFreshOnceExplicitlySet(t *testing.T) {
	// Once a session has been explicitly previewed against a workspace Markdown
	// file (workspaceOwned), the poller must still keep that preview fresh — the
	// document-fallback restriction only applies to never-previewed sessions.
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "docs", "report.md"), "# report")
	relative := "docs/report.md"
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, relative)}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}
	assertSets(t, svc.sets, previewSet{
		id:  "ao-1",
		url: mustFileURL(t, "http://127.0.0.1:3001", "ao-1", relative),
	})
}

func TestPollerSkipsNonWorkerSessions(t *testing.T) {
	workspace := t.TempDir()
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>hello</main>")
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{{
		ID:   "ao-orch",
		Kind: domain.KindOrchestrator,
		Metadata: domain.SessionMetadata{
			WorkspacePath: workspace,
		},
	}}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})

	if err := poller.Poll(context.Background()); err != nil {
		t.Fatalf("Poll: %v", err)
	}

	if len(svc.sets) != 0 {
		t.Fatalf("sets = %#v, want no preview updates for orchestrator sessions", svc.sets)
	}
}

func TestPollerForgetsTerminatedWorkerWithClearedPreview(t *testing.T) {
	workspace := t.TempDir()
	svc := &fakePreviewSessions{sessions: []domain.SessionRecord{workerSession("ao-1", workspace, "index.html")}}
	poller := NewPoller(svc, svc, "http://127.0.0.1:3001", PollerConfig{Logger: discardLogger()})
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSets(t, svc.sets, previewSet{id: "ao-1", url: ""})

	svc.sessions[0].IsTerminated = true
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(poller.seen) != 0 {
		t.Fatalf("terminated preview still tracked: %v", poller.seen)
	}
	writeFile(t, filepath.Join(workspace, "index.html"), "<main>back</main>")
	if err := poller.Poll(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertSets(t, svc.sets, previewSet{id: "ao-1", url: ""})
}

func workerSession(id domain.SessionID, workspace, previewURL string) domain.SessionRecord {
	return domain.SessionRecord{
		ID:   id,
		Kind: domain.KindWorker,
		Metadata: domain.SessionMetadata{
			WorkspacePath: workspace,
			PreviewURL:    previewURL,
		},
	}
}

func writeFile(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func assertSets(t *testing.T, got []previewSet, want ...previewSet) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("sets = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("sets[%d] = %#v, want %#v", i, got[i], want[i])
		}
	}
}

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}
