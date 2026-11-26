package session

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
)

func TestWorkspaceDiffStatsMatchesSeparateGitReads(t *testing.T) {
	root := newWorkspaceRepo(t)
	odd := "odd\tname\nfile.txt"
	if runtime.GOOS == "windows" {
		odd = "odd name.txt" // Win32 filenames cannot contain control characters.
	}
	writeWorkspaceFile(t, root, odd, "old\n")
	writeWorkspaceFile(t, root, "binary.dat", "old\x00data")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "fixtures")
	base := strings.TrimSpace(runGit(t, root, "rev-parse", "HEAD"))
	runGit(t, root, "mv", "README.md", "renamed.txt")
	writeWorkspaceFile(t, root, odd, "new\nextra\n")
	writeWorkspaceFile(t, root, "binary.dat", "new\x00data")
	runGit(t, root, "rm", "src/app.go")
	runGit(t, root, "add", ".")
	writeWorkspaceFile(t, root, "renamed.txt", "hello\nunstaged\n")
	for _, revisions := range [][]string{nil, {"--cached"}, {base}} {
		t.Run(strings.Join(revisions, "/"), func(t *testing.T) {
			statuses, previous, counts, err := workspaceDiffStats(t.Context(), root, revisions...)
			if err != nil {
				t.Fatal(err)
			}
			wantStatuses, wantPrevious, err := workspaceDiffNameStatus(t.Context(), root, revisions...)
			if err != nil {
				t.Fatal(err)
			}
			if len(revisions) > 0 && counts[odd] != [2]int{2, 1} {
				t.Fatalf("tab/newline path lost its line counts: %v", counts)
			}
			wantCounts, err := workspaceDiffNumstat(t.Context(), root, revisions...)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(statuses, wantStatuses) || !reflect.DeepEqual(previous, wantPrevious) || !reflect.DeepEqual(counts, wantCounts) {
				t.Fatalf("combined Git read = %v %v %v; separate = %v %v %v", statuses, previous, counts, wantStatuses, wantPrevious, wantCounts)
			}
		})
	}
	trace := filepath.Join(t.TempDir(), "trace.log")
	t.Setenv("GIT_TRACE", trace)
	if _, _, _, err := workspaceDiffStats(t.Context(), root, base); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(trace)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), "built-in: git diff") != 1 {
		t.Fatalf("want one Git traversal: %s", data)
	}
}
