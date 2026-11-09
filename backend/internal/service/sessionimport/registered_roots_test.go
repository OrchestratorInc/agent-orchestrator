package sessionimport

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestRegisteredDiscoveryRootsAreExplicitAndConfined(t *testing.T) {
	base := t.TempDir()
	home, repo, managed := filepath.Join(base, "home"), filepath.Join(base, "repo"), filepath.Join(base, "ao")
	svc := &Service{home: home, excluded: []string{managed}}
	dests := &destinations{projects: []projectRoot{{path: repo}, {path: home}, {path: repo}, {path: managed}, {path: filepath.Join(managed, "worktree")}}}
	if got := svc.registeredDiscoveryRoots(dests); !reflect.DeepEqual(got, []string{repo}) {
		t.Fatalf("registered roots = %v", got)
	}
	inside := filepath.Join(repo, ".provider")
	outside := filepath.Join(base, "other")
	for _, dir := range []string{inside, outside} {
		if err := os.MkdirAll(dir, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	files := []sourceFiles{{root: inside, key: "native"}, {root: outside}, {root: ""}}
	if err := os.Symlink(outside, filepath.Join(repo, "escape")); err == nil {
		files = append(files, sourceFiles{root: filepath.Join(repo, "escape")})
	}
	got := registeredSources(files, []string{canonical(repo)})
	if len(got) != 1 || got[0].key != "native" {
		t.Fatalf("confined sources = %#v", got)
	}
}

func TestRepoLocalDiscoveryUsesEachNativeRoot(t *testing.T) {
	old := harnessImporters[domain.HarnessGemini]
	t.Cleanup(func() { harnessImporters[domain.HarnessGemini] = old })
	repos := []string{t.TempDir(), t.TempDir()}
	harnessImporters[domain.HarnessGemini] = harnessImporter{
		mode: domain.SessionModeTUI,
		discoverRegistered: func(_ context.Context, roots []string) ([]sourceFiles, error) {
			if len(roots) != 2 {
				t.Fatalf("registered roots = %v", roots)
			}
			files := make([]sourceFiles, 0, len(roots))
			for _, root := range roots {
				files = append(files, sourceFiles{harness: domain.HarnessGemini, root: root, paths: []string{"native"}, key: "native"})
			}
			return files, nil
		},
		read: func(_ context.Context, root string, paths ...string) (transcript, error) {
			return transcript{harness: domain.HarnessGemini, root: root, path: paths[0], paths: paths, nativeID: "native", cwd: root, last: time.Now(), messages: []Message{{Role: domain.MessageRoleUser, Text: "synthetic"}}}, nil
		},
	}
	st := &importStore{projects: []domain.ProjectRecord{{ID: "one", Path: repos[0]}, {ID: "two", Path: repos[1]}}}
	svc := New(st, "", chatSupported)
	svc.roots = nil
	preview, err := svc.Scan(context.Background())
	if err != nil || len(preview.Candidates) != 2 || preview.Candidates[0].ID == preview.Candidates[1].ID {
		t.Fatalf("repo-local preview = %#v, %v", preview, err)
	}
	results, err := svc.Import(context.Background(), []string{preview.Candidates[0].ID})
	if err != nil || len(results) != 1 || results[0].Status != "created" || len(st.sessions) != 1 {
		t.Fatalf("repo-local import = %#v, %v", results, err)
	}
	if source := st.sessions[0].Metadata.ImportSource; source.ConfigDir != source.CWD {
		t.Fatalf("source root changed: %#v", source)
	}
	if st.sessions[0].Mode != domain.SessionModeTUI {
		t.Fatal("explicit Terminal import was sent to a registered Chat driver")
	}
}
