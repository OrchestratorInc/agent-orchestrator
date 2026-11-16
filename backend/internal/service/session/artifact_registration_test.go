package session

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

func registrationFixture(t *testing.T, files ...string) (*Service, *fakeStore, string) {
	t.Helper()
	dataDir := t.TempDir()
	dir := filepath.Join(dataDir, "artifacts", "mer-1")
	for _, rel := range files {
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	st := newFakeStore()
	st.sessions["mer-1"] = domain.SessionRecord{ID: "mer-1", ProjectID: "p", Metadata: domain.SessionMetadata{ArtifactDir: dir}}
	return NewWithDeps(Deps{Store: st, DataDir: dataDir}), st, dir
}

func TestRegisterArtifactsReportsWhatStaysUnlisted(t *testing.T) {
	svc, st, dir := registrationFixture(t, "brand.html", "draft.md", "assets/a.png", "assets/b.png")

	got, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{filepath.Join(dir, "brand.html")})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(got.Registered, []string{"brand.html"}) || got.OtherFiles != 3 || !slices.Equal(got.OtherRootFiles, []string{"draft.md"}) {
		t.Fatalf("registration = %+v", got)
	}
	if paths := st.artifacts["mer-1"]; !slices.Equal(paths, []string{"brand.html"}) {
		t.Fatalf("stored paths = %v", paths)
	}
}

func TestRegisterArtifactsReportsOnlyNewFilesBySourceCategoryAndSize(t *testing.T) {
	_, st, dir := registrationFixture(t, "brand.html", "notes.html", "shot.png", "app.icon/icon.json", "app.icon/a.png")
	big := filepath.Join(dir, "deck.pdf")
	if err := os.WriteFile(big, make([]byte, 200*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	rec := st.sessions["mer-1"]
	rec.Kind, rec.RequestedOutput = domain.KindWorker, domain.RequestedOutputArtifact
	st.sessions["mer-1"] = rec
	sink := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Store: st, DataDir: filepath.Dir(filepath.Dir(dir)), Telemetry: sink})
	ctx := context.Background()

	if _, err := svc.RegisterArtifacts(ctx, "mer-1", []string{"brand.html", "notes.html", "shot.png", "app.icon", "deck.pdf", "brand.html"}); err != nil {
		t.Fatal(err)
	}
	got := map[string]any{}
	for _, ev := range sink.events {
		if ev.Name != "ao.artifacts.registered" {
			t.Fatalf("unexpected event %q", ev.Name)
		}
		if len(ev.Payload) != 4 || ev.Payload["source"] != "artifacts_tab" || ev.SessionID != nil || ev.ProjectID != nil {
			t.Fatalf("event carries more than its source, category, size and count: %+v", ev)
		}
		got[ev.Payload["content_category"].(string)+"/"+ev.Payload["size_bucket"].(string)] = ev.Payload["count"]
	}
	// The bundle directory is one artifact, sized by the files in it.
	want := map[string]any{"html/xs": 2, "image/xs": 1, "other/xs": 1, "pdf/m": 1}
	if !maps.Equal(got, want) {
		t.Fatalf("counts = %v, want %v", got, want)
	}

	// Registering a path again is not a new artifact.
	sink.events = nil
	if _, err := svc.RegisterArtifacts(ctx, "mer-1", []string{"brand.html"}); err != nil {
		t.Fatal(err)
	}
	if len(sink.events) != 0 {
		t.Fatalf("re-registration sent %+v", sink.events)
	}
}

func TestRegisterArtifactsReportsWhyARegistrationWasRefused(t *testing.T) {
	_, st, dir := registrationFixture(t, "brand.html")
	if err := os.MkdirAll(filepath.Join(dir, "folder"), 0o700); err != nil {
		t.Fatal(err)
	}
	sink := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Store: st, DataDir: filepath.Dir(filepath.Dir(dir)), Telemetry: sink})
	for reference, reason := range map[string]string{
		"/etc/passwd": "outside_dir",
		"missing.md":  "not_found",
		"folder":      "not_registrable",
	} {
		sink.events = nil
		if _, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{reference}); err == nil {
			t.Fatalf("registered %q", reference)
		}
		if len(sink.events) != 1 || sink.events[0].Name != "ao.artifacts.register_failed" ||
			!maps.Equal(sink.events[0].Payload, map[string]any{"source": "worker", "reason": reason}) {
			t.Fatalf("%q sent %+v", reference, sink.events)
		}
	}
}

// goneStore has the session deleted between reading its record and
// registering its artifacts.
type goneStore struct{ *fakeStore }

func (goneStore) AddSessionArtifacts(context.Context, domain.SessionID, []domain.SessionArtifactRegistration, time.Time) ([]string, bool, error) {
	return nil, false, nil
}

func TestRegisterArtifactsReportsASessionDeletedMidRegistration(t *testing.T) {
	_, st, dir := registrationFixture(t, "brand.html")
	sink := &fakeTelemetrySink{}
	svc := NewWithDeps(Deps{Store: goneStore{st}, DataDir: filepath.Dir(filepath.Dir(dir)), Telemetry: sink})
	if _, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{"brand.html"}); err == nil {
		t.Fatal("registered artifacts of a deleted session")
	}
	if len(sink.events) != 1 || sink.events[0].Name != "ao.artifacts.register_failed" ||
		!maps.Equal(sink.events[0].Payload, map[string]any{"source": "worker", "reason": "session_not_found"}) {
		t.Fatalf("sent %+v", sink.events)
	}
}

func TestRegisterArtifactsRegistersNothingUnlessEveryPathIsValid(t *testing.T) {
	svc, st, _ := registrationFixture(t, "brand.html")

	_, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{"brand.html", "gone.md"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "ARTIFACT_FILE_NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
	if paths := st.artifacts["mer-1"]; len(paths) != 0 {
		t.Fatalf("stored paths = %v, want none", paths)
	}
}

func TestRegisterArtifactsRefusesPathsOutsideTheDirectory(t *testing.T) {
	svc, _, _ := registrationFixture(t, "brand.html")

	_, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{"../other/secret.md"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "ARTIFACT_PATH_OUTSIDE_DIR" {
		t.Fatalf("err = %v", err)
	}
}

func TestRegisterArtifactsAcceptsAPathAfterManyRenames(t *testing.T) {
	svc, st, _ := registrationFixture(t, "brand-v2.html")
	renamed := make([]string, 2*domain.MaxArtifactRegistrationPaths)
	for i := range renamed {
		renamed[i] = fmt.Sprintf("renamed-%d.html", i)
	}
	st.artifacts = map[domain.SessionID][]string{"mer-1": renamed}

	if _, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{"brand-v2.html"}); err != nil {
		t.Fatalf("rows left by renamed files refused a new path: %v", err)
	}
	if paths := st.artifacts["mer-1"]; !slices.Contains(paths, "brand-v2.html") {
		t.Fatalf("stored paths = %v", paths)
	}
}

func TestGetMarksRegisteredArtifactFiles(t *testing.T) {
	svc, st, _ := registrationFixture(t, "brand.html", "assets/a.png")
	st.artifacts = map[domain.SessionID][]string{"mer-1": {"brand.html", "renamed-away.html"}}

	sess, err := svc.Get(context.Background(), "mer-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(sess.ArtifactFiles) != 2 {
		t.Fatalf("files = %+v, want every file", sess.ArtifactFiles)
	}
	for _, file := range sess.ArtifactFiles {
		if file.Registered != (file.Path == "brand.html") {
			t.Errorf("%s registered = %v", file.Path, file.Registered)
		}
	}
}
