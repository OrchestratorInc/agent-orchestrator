package lifecycle

import (
	"context"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestApplyPRObservation_PersistsPROutputType(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = working("mer-1")
	st.prs["mer-1"] = []domain.PullRequest{{URL: "pr1"}}

	if err := m.ApplyPRObservation(ctx, "mer-1", ports.PRObservation{Fetched: true, URL: "pr1"}); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].OutputType; got != domain.SessionOutputPR {
		t.Fatalf("outputType = %q, want %q", got, domain.SessionOutputPR)
	}
}

func TestReconcileSessionOutputType_ArtifactFilesPersistArtifactOutput(t *testing.T) {
	m, st, _ := newManager()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:       "mer-1",
		Metadata: domain.SessionMetadata{ArtifactDir: dir},
	}

	if err := m.ReconcileSessionOutputType(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].OutputType; got != domain.SessionOutputArtifact {
		t.Fatalf("outputType = %q, want %q", got, domain.SessionOutputArtifact)
	}
}

func TestReconcileSessionOutputType_PRAndArtifactFilesCombine(t *testing.T) {
	m, st, _ := newManager()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:       "mer-1",
		Metadata: domain.SessionMetadata{ArtifactDir: dir},
	}
	st.prs["mer-1"] = []domain.PullRequest{{URL: "https://example.com/pr/1"}}

	if err := m.ReconcileSessionOutputType(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].OutputType; got != domain.SessionOutputPRAndArtifact {
		t.Fatalf("outputType = %q, want %q", got, domain.SessionOutputPRAndArtifact)
	}
}

func TestReconcileSessionOutputType_BackfillsEmptyArtifactDir(t *testing.T) {
	dataDir := t.TempDir()
	m, st, _ := newManager(WithDataDir(dataDir))
	artifactDir := filepath.Join(dataDir, "artifacts", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "report.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:       "mer-1",
		Metadata: domain.SessionMetadata{ArtifactDir: ""},
	}

	if err := m.ReconcileSessionOutputType(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.Metadata.ArtifactDir != artifactDir {
		t.Fatalf("artifactDir = %q, want backfilled %q", got.Metadata.ArtifactDir, artifactDir)
	}
	if got.OutputType != domain.SessionOutputArtifact {
		t.Fatalf("outputType = %q, want %q", got.OutputType, domain.SessionOutputArtifact)
	}
}

func TestReconcileSessionOutputType_NoOpWithoutDataDirConfigured(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:         "mer-1",
		OutputType: domain.SessionOutputNone,
	}

	if err := m.ReconcileSessionOutputType(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	got := st.sessions["mer-1"]
	if got.Metadata.ArtifactDir != "" {
		t.Fatalf("artifactDir = %q, want still empty without a configured dataDir", got.Metadata.ArtifactDir)
	}
}

func TestReconcileSessionOutputType_NoOpWhenUnchanged(t *testing.T) {
	m, st, _ := newManager()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:         "mer-1",
		OutputType: domain.SessionOutputNone,
	}

	if err := m.ReconcileSessionOutputType(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].OutputType; got != domain.SessionOutputNone {
		t.Fatalf("outputType = %q, want %q", got, domain.SessionOutputNone)
	}
}

func TestReconcileSessionOutputType_UnknownSessionIsNoOp(t *testing.T) {
	m, _, _ := newManager()
	if err := m.ReconcileSessionOutputType(ctx, "missing"); err != nil {
		t.Fatal(err)
	}
}

type staleReadStore struct {
	*fakeStore
	staleRead domain.SessionRecord
}

func (s *staleReadStore) GetSession(context.Context, domain.SessionID) (domain.SessionRecord, bool, error) {
	return s.staleRead, true, nil
}

func TestReconcileSessionOutputType_DoesNotResurrectSessionTerminatedDuringRead(t *testing.T) {
	dataDir := t.TempDir()
	artifactDir := filepath.Join(dataDir, "artifacts", "mer-1")
	if err := os.MkdirAll(artifactDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(artifactDir, "report.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}

	preTermination := domain.SessionRecord{
		ID:           "mer-1",
		IsTerminated: false,
		Activity:     domain.Activity{State: domain.ActivityActive, LastActivityAt: time.Now()},
		Metadata: domain.SessionMetadata{
			RuntimeHandleID: "handle-old",
			AgentSessionID:  "agent-old",
			PreviewURL:      "http://old-preview",
		},
	}

	terminated := preTermination
	terminated.IsTerminated = true
	terminated.Activity = domain.Activity{State: domain.ActivityExited, LastActivityAt: time.Now()}
	terminated.Metadata.RuntimeHandleID = ""
	terminated.Metadata.AgentSessionID = "agent-new"
	terminated.Metadata.PreviewURL = "http://new-preview"

	st := newFakeStore()
	st.sessions["mer-1"] = terminated
	wrapped := &staleReadStore{fakeStore: st, staleRead: preTermination}
	m := New(wrapped, &fakeMessenger{}, WithDataDir(dataDir))

	if err := m.ReconcileSessionOutputType(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}

	got := st.sessions["mer-1"]
	if !got.IsTerminated {
		t.Fatal("IsTerminated reverted to false: reconcile resurrected a session terminated during its read")
	}
	if got.Activity.State != domain.ActivityExited {
		t.Fatalf("Activity.State = %q, want %q (stale pre-termination activity must not be replayed)", got.Activity.State, domain.ActivityExited)
	}
	if got.Metadata.RuntimeHandleID != "" {
		t.Fatalf("RuntimeHandleID = %q, want cleared (stale value must not be replayed)", got.Metadata.RuntimeHandleID)
	}
	if got.Metadata.AgentSessionID != "agent-new" {
		t.Fatalf("AgentSessionID = %q, want %q", got.Metadata.AgentSessionID, "agent-new")
	}
	if got.Metadata.PreviewURL != "http://new-preview" {
		t.Fatalf("PreviewURL = %q, want %q", got.Metadata.PreviewURL, "http://new-preview")
	}
	if got.Metadata.ArtifactDir != artifactDir {
		t.Fatalf("ArtifactDir = %q, want backfilled %q", got.Metadata.ArtifactDir, artifactDir)
	}
	if got.OutputType != domain.SessionOutputArtifact {
		t.Fatalf("OutputType = %q, want %q", got.OutputType, domain.SessionOutputArtifact)
	}
}

func TestReconcileSessionOutputType_DeletingLastArtifactDropsBackToNone(t *testing.T) {
	m, st, _ := newManager()
	dir := t.TempDir()
	st.sessions["mer-1"] = domain.SessionRecord{
		ID:         "mer-1",
		OutputType: domain.SessionOutputArtifact,
		Metadata:   domain.SessionMetadata{ArtifactDir: dir},
	}

	if err := m.ReconcileSessionOutputType(ctx, "mer-1"); err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].OutputType; got != domain.SessionOutputNone {
		t.Fatalf("outputType = %q, want %q after the last artifact was removed", got, domain.SessionOutputNone)
	}
}

type gatedListStore struct {
	*fakeStore
	calls   int32
	entered chan struct{}
	release chan struct{}
}

func (s *gatedListStore) ListPRsBySession(ctx context.Context, id domain.SessionID) ([]domain.PullRequest, error) {
	prs, err := s.fakeStore.ListPRsBySession(ctx, id)
	if atomic.AddInt32(&s.calls, 1) == 1 {
		close(s.entered)
		<-s.release
	}
	return prs, err
}

func TestReconcileSessionOutputType_OverlappingReconcilesCannotPersistStaleScan(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.html"), []byte("<html></html>"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", Metadata: domain.SessionMetadata{ArtifactDir: dir}}
	gated := &gatedListStore{fakeStore: st, entered: make(chan struct{}), release: make(chan struct{})}
	m := New(gated, &fakeMessenger{})

	older := make(chan error, 1)
	go func() { older <- m.ReconcileSessionOutputType(ctx, "mer-1") }()
	<-gated.entered

	st.prs["mer-1"] = []domain.PullRequest{{URL: "https://github.com/acme/repo/pull/1", Number: 1}}
	newer := make(chan error, 1)
	go func() { newer <- m.ReconcileSessionOutputType(ctx, "mer-1") }()

	select {
	case <-newer:
		t.Fatal("a second reconcile ran while the first was mid-scan; stale writes are possible")
	case <-time.After(25 * time.Millisecond):
	}
	close(gated.release)
	if err := <-older; err != nil {
		t.Fatal(err)
	}
	if err := <-newer; err != nil {
		t.Fatal(err)
	}
	if got := st.sessions["mer-1"].OutputType; got != domain.SessionOutputPRAndArtifact {
		t.Fatalf("outputType = %q, want %q (the newer scan must win)", got, domain.SessionOutputPRAndArtifact)
	}
}
