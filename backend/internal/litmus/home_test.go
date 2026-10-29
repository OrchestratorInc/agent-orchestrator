package litmus

import (
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHomeSnapshotDepthMtimesExternalRootsAndSymlinks(t *testing.T) {
	home, bin, cellar := t.TempDir(), t.TempDir(), t.TempDir()
	writeFile(t, filepath.Join(home, "profile", "config"), "private contents")
	writeFile(t, filepath.Join(home, "profile", "nested", "outside-depth"), "private contents")
	writeFile(t, filepath.Join(home, "excluded", "data"), "runtime")
	writeFile(t, filepath.Join(bin, "tool"), "executable")
	writeFile(t, filepath.Join(cellar, "package", "version", "file"), "installed")
	if err := os.Symlink(bin, filepath.Join(home, "link")); err != nil {
		t.Fatal(err)
	}
	roots := []homeRoot{{home, 2}, {bin, 1}, {cellar, 1}}
	exclude := filepath.Join(home, "excluded")
	before := snapshotHome(roots, exclude)
	if len(before.Errors) != 0 {
		t.Fatal(before.Errors)
	}
	for _, path := range []string{filepath.Join(home, "profile", "config"), filepath.Join(bin, "tool"), filepath.Join(cellar, "package")} {
		if _, ok := before.Entries[path]; !ok {
			t.Errorf("missing snapshot entry %s", path)
		}
	}
	for _, path := range []string{exclude, filepath.Join(home, "profile", "nested", "outside-depth"), filepath.Join(home, "link", "tool"), filepath.Join(cellar, "package", "version")} {
		if _, ok := before.Entries[path]; ok {
			t.Errorf("snapshot crossed depth, exclusion or symlink boundary: %s", path)
		}
	}
	changed := filepath.Join(home, "profile", "config")
	mtime := time.Unix(123, 456)
	if err := os.Chtimes(changed, mtime, mtime); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(bin, "tool")); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cellar, "added"), "new")
	changes := homeChanges(before, snapshotHome(roots, exclude))
	for _, want := range []string{"~ " + changed, "- " + filepath.Join(bin, "tool"), "+ " + filepath.Join(cellar, "added")} {
		if !slices.Contains(changes, want) {
			t.Errorf("changes omit %q: %v", want, changes)
		}
	}
	// The report is sorted by path, independent of change kind.
	paths := make([]string, len(changes))
	for i, line := range changes {
		paths[i] = line[2:]
	}
	if !slices.IsSorted(paths) {
		t.Fatalf("changes are not sorted by path: %v", changes)
	}
}

func TestTeardownReportsHomeChangesAndPrivateOrSharedEnvironment(t *testing.T) {
	for _, private := range []bool{true, false} {
		t.Run(map[bool]string{true: "private", false: "shared"}[private], func(t *testing.T) {
			home := t.TempDir()
			writeFile(t, filepath.Join(home, "kept"), "not read")
			svc := New(NewStore(t.TempDir()), nil)
			svc.sys = (&fakeSys{}).ops()
			svc.closeEngine = func(string) {}
			run := Run{ID: "run_home", Repo: "o/r", PRNumber: 7, State: RunRunning}
			is(t, "")(nil, svc.store.Save(run))
			before := snapshotHome([]homeRoot{{home, 2}}, svc.store.dataDir())
			is(t, "")(nil, svc.store.put(run.ID, "home-before.json", before))
			writeFile(t, filepath.Join(home, "new"), "not read")
			// Another leg must keep the first leg's baseline.
			svc.snapshotHomeBefore(run.ID)
			dir := svc.store.legDir(run.ID, "head")
			for _, sub := range []string{"home", "tmp", "logs"} {
				is(t, "")(nil, os.MkdirAll(filepath.Join(dir, sub), 0o700))
			}
			is(t, "")(nil, writeOwned(dir, Owned{RunID: run.ID, Leg: "head", State: "running", PrivateHome: boolPtr(private)}))
			is(t, "")(nil, svc.teardown(ctx, run))
			if pathExists(filepath.Join(dir, "home")) || pathExists(filepath.Join(dir, "tmp")) || !pathExists(filepath.Join(dir, "logs")) {
				t.Fatal("teardown did not remove private state and keep logs")
			}
			data := readRunFile(t, svc, run, "home-changes.txt")
			if !strings.HasPrefix(data, homeChangeNotice+"\n") || !strings.Contains(data, "+ "+filepath.Join(home, "new")) {
				t.Fatalf("teardown lost the pre-launch baseline: %s", data)
			}
			want := "legs ran with the owner's HOME"
			if private {
				want = "legs ran with a private HOME for $HOME-based state; no owner provider state was copied in; provider login state was not checked"
			}
			body := renderVerdict(run, Verdict{Result: "blocked", Environment: "tester environment"}, nil, filepath.Join(svc.store.root, run.ID), "", evidence{})
			if !strings.Contains(body, want) || !strings.Contains(body, "Home changes (not attributed): ") || !strings.Contains(body, "tester environment") {
				t.Fatalf("environment report omits Go observation or tester text:\n%s", body)
			}
		})
	}
}

func TestHomeSnapshotPermissionErrorsAreUnreadableOnce(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EPERM, syscall.EACCES, syscall.EIO} {
		t.Run(errno.Error(), func(t *testing.T) {
			home := t.TempDir()
			svc := New(NewStore(t.TempDir()), nil)
			run := Run{ID: "run_permission"}
			is(t, "")(nil, svc.store.Save(run))
			before := snapshotHome([]homeRoot{{home, 0}}, svc.store.dataDir())
			path := filepath.Join(home, "protected")
			for range 2 {
				before.recordError(&fs.PathError{Op: "readdir", Path: path, Err: errno})
			}
			is(t, "")(nil, svc.store.put(run.ID, "home-before.json", before))
			svc.store.reportHomeChanges(run.ID)
			diagnostics := readRunFile(t, svc, run, "home-snapshot-errors.txt")
			if strings.Count(diagnostics, path) != 1 {
				t.Fatalf("diagnostic was omitted or repeated: %s", diagnostics)
			}
			permission := errno == syscall.EPERM || errno == syscall.EACCES
			if strings.HasPrefix(diagnostics, "unreadable: ") != permission {
				t.Fatalf("wrong error classification: %s", diagnostics)
			}
			body := renderVerdict(run, Verdict{}, nil, filepath.Join(svc.store.root, run.ID), "", evidence{})
			if strings.Contains(body, "Home changes may be incomplete") == permission {
				t.Fatalf("wrong incomplete note for %v: %s", errno, body)
			}
			if !strings.Contains(body, "Home changes (not attributed): 0") {
				t.Fatalf("report header was counted as a change: %s", body)
			}
		})
	}
}

func TestSnapshotHomeClassifiesUnreadableDirectory(t *testing.T) {
	home := t.TempDir()
	protected := filepath.Join(home, "protected")
	is(t, "")(nil, os.Mkdir(protected, 0o700))
	is(t, "")(nil, os.Chmod(protected, 0))
	t.Cleanup(func() { is(t, "")(nil, os.Chmod(protected, 0o700)) })
	snapshot := snapshotHome([]homeRoot{{home, 2}}, "")
	if len(snapshot.Errors) != 0 || !slices.Contains(snapshot.Unreadable, "unreadable: "+protected) {
		t.Fatalf("unreadable directory was not classified: %+v", snapshot)
	}
}

func TestHomeSnapshotReadFailureDoesNotBlockTeardown(t *testing.T) {
	svc := New(NewStore(t.TempDir()), nil)
	svc.sys = (&fakeSys{}).ops()
	svc.closeEngine = func(string) {}
	run := Run{ID: "run_bad_snapshot"}
	is(t, "")(nil, svc.store.Save(run))
	is(t, "")(nil, svc.store.WriteFile(run.ID, "home-before.json", []byte("{bad")))
	is(t, "")(nil, svc.teardown(ctx, run))
	if !strings.Contains(readRunFile(t, svc, run, "home-snapshot-errors.txt"), "invalid character") {
		t.Fatal("snapshot failure was not reported")
	}
}

func TestRemoveLegAliasesUsesOwnershipRatherThanAppNames(t *testing.T) {
	root, leg, foreign := t.TempDir(), t.TempDir(), t.TempDir()
	for name, target := range map[string]string{"any-app": leg, "nested": filepath.Join(leg, "socket"), "foreign": foreign} {
		is(t, "")(nil, os.Symlink(target, filepath.Join(root, name)))
	}
	writeFile(t, filepath.Join(root, "ordinary-file"), "keep")
	is(t, "")(nil, removeLegAliases(leg, []string{root}))
	for _, name := range []string{"any-app", "nested"} {
		if _, err := os.Lstat(filepath.Join(root, name)); !os.IsNotExist(err) {
			t.Fatalf("owned alias %s remains: %v", name, err)
		}
	}
	for _, name := range []string{"foreign", "ordinary-file"} {
		if _, err := os.Lstat(filepath.Join(root, name)); err != nil {
			t.Fatalf("unowned path %s removed: %v", name, err)
		}
	}
}
