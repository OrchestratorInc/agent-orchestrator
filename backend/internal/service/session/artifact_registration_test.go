package session

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"testing"

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
	if paths := st.sessions["mer-1"].RegisteredArtifactPaths; !slices.Equal(paths, []string{"brand.html"}) {
		t.Fatalf("stored paths = %v", paths)
	}
}

func TestRegisterArtifactsRegistersNothingUnlessEveryPathIsValid(t *testing.T) {
	svc, st, _ := registrationFixture(t, "brand.html")

	_, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{"brand.html", "gone.md"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "ARTIFACT_FILE_NOT_FOUND" {
		t.Fatalf("err = %v", err)
	}
	if paths := st.sessions["mer-1"].RegisteredArtifactPaths; len(paths) != 0 {
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

func TestRegisterArtifactsPastTheLimit(t *testing.T) {
	svc, st, _ := registrationFixture(t, "brand.html")
	rec := st.sessions["mer-1"]
	for i := range domain.MaxRegisteredArtifactPaths {
		rec.RegisteredArtifactPaths = append(rec.RegisteredArtifactPaths, fmt.Sprintf("f%d.md", i))
	}
	st.sessions["mer-1"] = rec

	_, err := svc.RegisterArtifacts(context.Background(), "mer-1", []string{"brand.html"})
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "ARTIFACT_REGISTRATION_LIMIT" {
		t.Fatalf("err = %v", err)
	}
}

func TestGetMarksRegisteredArtifactFiles(t *testing.T) {
	svc, st, _ := registrationFixture(t, "brand.html", "assets/a.png")
	rec := st.sessions["mer-1"]
	rec.RegisteredArtifactPaths = []string{"brand.html", "renamed-away.html"}
	st.sessions["mer-1"] = rec

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
