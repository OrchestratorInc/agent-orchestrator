package litmus

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// pruneFixture lays out three checkouts, four runs and an evidence clone under a temp data dir.
// c1 is referenced by two old finished runs, c2 by the recent finished run, c3 by a running run.
func pruneFixture(t *testing.T) (svc *Service, root string, recent Run) {
	t.Helper()
	svc = New(NewStore(t.TempDir()), nil)
	root = filepath.Dir(svc.store.root)
	c1, c2, c3 := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	for _, sha := range []string{c1, c2, c3} {
		dir := filepath.Join(root, "repos", "aoagents__agent-orchestrator", sha)
		writeFile(t, filepath.Join(dir, "node_modules", "big.bin"), strings.Repeat("x", 1000))
		old := time.Now().Add(-48 * time.Hour)
		if err := os.Chtimes(dir, old, old); err != nil { // a checkout nobody used lately
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(root, "evidence", "aoagents__agent-orchestrator", "runs", "1", "a.png"), "png")
	day := 24 * time.Hour
	base := time.Date(2026, 10, 10, 9, 0, 0, 0, time.UTC)
	for _, r := range []Run{
		{ID: "run_old1", State: RunCleaned, CreatedAt: base.Add(-4 * day), HeadSHA: c1, BaseSHA: c1},
		{ID: "run_old2", State: RunSubmitted, CreatedAt: base.Add(-3 * day), HeadSHA: c1, BaseSHA: c1},
		{ID: "run_live", State: RunRunning, CreatedAt: base.Add(-2 * day), HeadSHA: c3, BaseSHA: c3},
		{ID: "run_recent", State: RunSubmitted, CreatedAt: base.Add(-day), HeadSHA: c2, BaseSHA: c2},
	} {
		r.Repo = "aoagents/agent-orchestrator"
		if err := svc.store.Save(r); err != nil {
			t.Fatal(err)
		}
		writeFile(t, filepath.Join(svc.store.root, r.ID, "verdict.md"), "v")
		if r.ID == "run_recent" {
			recent = r
		}
	}
	return svc, root, recent
}

func TestPruneDryRunDeletesNothing(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	rep, err := svc.PruneDryRun(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !rep.DryRun || len(rep.Checkouts) != 1 || len(rep.Runs) != 2 || rep.BytesFreed < 1000 {
		t.Fatalf("report = %+v", rep)
	}
	if !strings.HasPrefix(rep.Lines[0], "would remove checkout repos/") || !strings.HasPrefix(rep.Lines[len(rep.Lines)-1], "would prune: 1 checkouts, 2 runs") {
		t.Fatalf("lines = %q", rep.Lines)
	}
	for _, p := range append(append([]string{}, rep.Checkouts...), rep.Runs...) {
		if !pathExists(filepath.Join(root, filepath.FromSlash(p))) {
			t.Fatalf("dry run deleted %s", p)
		}
	}
}

func TestPruneRemovesExactlyTheStaleDirs(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	rep, err := svc.Prune(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repos", "aoagents__agent-orchestrator")
	for path, want := range map[string]bool{
		filepath.Join(repo, strings.Repeat("1", 40)):                                          false, // only old finished runs
		filepath.Join(repo, strings.Repeat("2", 40)):                                          true,  // the newest finished run
		filepath.Join(repo, strings.Repeat("3", 40)):                                          true,  // a running run, however old
		filepath.Join(svc.store.root, "run_old1"):                                             false,
		filepath.Join(svc.store.root, "run_old2"):                                             false,
		filepath.Join(svc.store.root, "run_live"):                                             true,
		filepath.Join(svc.store.root, "run_recent"):                                           true,
		filepath.Join(root, "evidence", "aoagents__agent-orchestrator", "runs", "1", "a.png"): true,
	} {
		if got := pathExists(path); got != want {
			t.Errorf("%s exists = %v, want %v", strings.TrimPrefix(path, root), got, want)
		}
	}
	wantRel := "repos/aoagents__agent-orchestrator/" + strings.Repeat("1", 40)
	if len(rep.Checkouts) != 1 || rep.Checkouts[0] != wantRel || len(rep.Runs) != 2 || rep.BytesFreed < 1000 {
		t.Fatalf("report = %+v", rep)
	}
	if got := rep.Lines[len(rep.Lines)-1]; got != "pruned: 1 checkouts, 2 runs, 0.0 GB freed" {
		t.Fatalf("summary = %q", got)
	}
}

func TestPruneKeepsEveryCheckoutWhileARunIsUnfinished(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	if _, err := svc.Prune(ctx, 0, 0); err != nil { // keep nothing that is not needed
		t.Fatal(err)
	}
	repo := filepath.Join(root, "repos", "aoagents__agent-orchestrator")
	if !pathExists(filepath.Join(repo, strings.Repeat("3", 40))) || !pathExists(filepath.Join(svc.store.root, "run_live")) {
		t.Fatal("the running run lost its checkout or its dir")
	}
	if pathExists(filepath.Join(repo, strings.Repeat("1", 40))) || pathExists(filepath.Join(repo, strings.Repeat("2", 40))) {
		t.Fatal("finished runs' checkouts should go at keep 0")
	}
}

func TestPruneRunRetention(t *testing.T) {
	for _, tc := range []struct {
		name     string
		auto     bool
		keepRuns int
	}{
		{name: "automatic keeps all 25 runs", auto: true, keepRuns: 25},
		{name: "manual keeps newest 20 runs", keepRuns: 20},
		{name: "manual zero removes all finished runs"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(NewStore(t.TempDir()), nil)
			old := time.Now().Add(-48 * time.Hour)
			reviewFiles := []string{"verdict.md", "verdict.json", "comment.md", "steps.md", "evidence/shot.png", "receipts/000001.json", "head/head.mp4", "base/base.mp4", "clips/f1.mp4"}
			var runs []Run
			for i := range 25 {
				r := Run{ID: fmt.Sprintf("run_%02d", i), Repo: "acme/repo", State: RunSubmitted, CreatedAt: old.Add(time.Duration(i) * time.Minute), HeadSHA: fmt.Sprintf("%040x", i+1)}
				if err := svc.store.Save(r); err != nil {
					t.Fatal(err)
				}
				runs = append(runs, r)
				for _, name := range reviewFiles {
					writeFile(t, filepath.Join(svc.store.root, r.ID, name), name)
				}
				for _, leg := range []string{"head", "base"} {
					legDir := filepath.Join(svc.store.root, r.ID, leg)
					for _, part := range []string{"frames", "home", "tmp", "data"} {
						writeFile(t, filepath.Join(legDir, part, "000001.jpg"), "jpeg")
					}
					other := filepath.Join(t.TempDir(), "other-run")
					writeFile(t, filepath.Join(other, "data", "keep.txt"), "keep")
					if err := os.Symlink(other, filepath.Join(legDir, "link")); err != nil {
						t.Fatal(err)
					}
					if err := writeOwned(legDir, Owned{Dirs: []string{filepath.Join(legDir, "data"), filepath.Join(t.TempDir(), "outside"), filepath.Join(legDir, "link", "data"), legDir}}); err != nil {
						t.Fatal(err)
					}
					t.Cleanup(func() {
						if !pathExists(filepath.Join(other, "data", "keep.txt")) {
							t.Errorf("prune followed a symlinked parent out of %s", legDir)
						}
					})
				}
				checkout := filepath.Join(svc.store.reposDir(r.Repo), r.HeadSHA)
				writeFile(t, filepath.Join(checkout, "node_modules", "big.bin"), "build")
				if err := os.Chtimes(checkout, old, old); err != nil {
					t.Fatal(err)
				}
			}
			asides := []string{filepath.Join(svc.store.root, "run_interrupted.pruning"), filepath.Join(svc.store.reposDir("acme/repo"), runs[24].HeadSHA+".pruning")}
			for _, aside := range asides {
				writeFile(t, filepath.Join(aside, "leftover.bin"), "leftover")
			}
			if tc.auto {
				svc.autoPrune(ctx, runs[24])
			} else if _, err := svc.Prune(ctx, tc.keepRuns, DefaultKeepCheckouts); err != nil {
				t.Fatal(err)
			}
			for i, r := range runs {
				runDir := filepath.Join(svc.store.root, r.ID)
				keep := i >= len(runs)-tc.keepRuns
				if got := pathExists(runDir); got != keep {
					t.Errorf("%s exists = %v, want %v", r.ID, got, keep)
				}
				if keep {
					for _, name := range append([]string{"run.json"}, reviewFiles...) {
						if !pathExists(filepath.Join(runDir, name)) {
							t.Errorf("%s lost %s", r.ID, name)
						}
					}
				}
				for _, leg := range []string{"head", "base"} {
					for _, part := range []string{"frames", "home", "tmp", "data"} {
						if pathExists(filepath.Join(runDir, leg, part)) {
							t.Errorf("%s kept %s %s", r.ID, leg, part)
						}
					}
				}
				if got, want := pathExists(filepath.Join(svc.store.reposDir(r.Repo), r.HeadSHA)), i >= len(runs)-DefaultKeepCheckouts; got != want {
					t.Errorf("%s checkout exists = %v, want %v", r.ID, got, want)
				}
			}
			for _, aside := range asides {
				if pathExists(aside) {
					t.Errorf("leftover aside dir survived: %s", aside)
				}
			}
		})
	}
}

func TestPruneRefusesPathsOutsideTheDataDir(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	outside := t.TempDir()
	writeFile(t, filepath.Join(outside, strings.Repeat("9", 40), "keep.txt"), "keep")
	old := time.Now().Add(-48 * time.Hour)
	if err := os.Chtimes(filepath.Join(outside, strings.Repeat("9", 40)), old, old); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "repos", "evil__repo")); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Prune(ctx, 1, 1); err == nil {
		t.Fatal("want an error for the symlinked repo dir")
	}
	if !pathExists(filepath.Join(outside, strings.Repeat("9", 40), "keep.txt")) {
		t.Fatal("deleted a dir outside the data dir")
	}
}

// Removing a checkout drops its pin from the mirror unless another checkout or an unfinished run shares the commit;
// a pin nothing needs any more goes too, and the mirror is cleaned once. A kept checkout keeps its pin.
func TestPruneDropsThePinsOfRemovedCheckoutsAndCleansTheMirrorOnce(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	c1, c2, c3, c4 := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40), strings.Repeat("4", 40)
	mirror := filepath.Join(root, "mirrors", "aoagents__agent-orchestrator.git")
	writeFile(t, filepath.Join(mirror, "HEAD"), "ref: refs/heads/main\n")
	f := &fakeRunner{gitOut: map[string]string{"for-each-ref --format=%(refname)": strings.Join([]string{keepRefs + c1, keepRefs + c2, keepRefs + c3, keepRefs + c4}, "\n") + "\n"}}
	svc.runner = f
	rep, err := svc.Prune(ctx, 1, 1) // c1's checkout goes (old finished runs), c2 stays (recent), c3 stays (running), c4 has no checkout and no run
	if err != nil || len(rep.Checkouts) != 1 {
		t.Fatalf("report %+v err %v", rep, err)
	}
	for sha, want := range map[string]int{c1: 1, c4: 1, c2: 0, c3: 0} {
		if got := f.count("update-ref -d " + keepRefs + sha); got != want {
			t.Errorf("pin of %.4s dropped %d times, want %d: %q", sha, got, want, f.calls)
		}
	}
	if f.count("gc --prune=now --quiet") != 1 || !strings.Contains(strings.Join(rep.Lines, "\n"), "dropped 2 pinned commits from mirrors/aoagents__agent-orchestrator.git and cleaned it") || pathExists(filepath.Join(mirror, gcDueFile)) {
		t.Fatalf("calls %q lines %q", f.calls, rep.Lines)
	}
	if !pathExists(mirror) {
		t.Fatal("prune removed the mirror")
	}
	f.gitOut["for-each-ref --format=%(refname)"] = keepRefs + c2 + "\n" + keepRefs + c3 + "\n"
	before := f.count("gc")
	if _, err := svc.Prune(ctx, 1, 1); err != nil || f.count("gc") != before { // nothing to drop: no cleanup
		t.Fatalf("a prune with nothing to unpin cleaned the mirror: %v", err)
	}
	if rep, err := svc.PruneDryRun(ctx, 0, 0); err != nil || f.count("update-ref -d") != 2 || len(rep.Checkouts) == 0 {
		t.Fatalf("a dry run touched the pins: %+v %v", rep, err)
	}
}

func TestPruneRejectsNegativeKeep(t *testing.T) {
	svc, _, _ := pruneFixture(t)
	is(t, "TESTING_PRUNE_INVALID")(svc.Prune(ctx, -1, 2))
}

func TestAutoPruneLogsOneLineToCommentLog(t *testing.T) {
	svc, _, recent := pruneFixture(t)
	svc.keepRuns, svc.keepCheckouts = 1, 1
	svc.autoPrune(ctx, recent)
	log, err := os.ReadFile(filepath.Join(svc.store.root, recent.ID, "comment.log"))
	if err != nil || !strings.HasSuffix(strings.TrimSpace(string(log)), "pruned: 1 checkouts, 0 runs, 0.0 GB freed") {
		t.Fatalf("comment.log = %q, %v", log, err)
	}
}

func TestPruneKeepsACheckoutAFreshRunPinnedAfterThePlan(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	c1 := filepath.Join(root, "repos", "aoagents__agent-orchestrator", strings.Repeat("1", 40))
	pruneSeam = func() { // a run starts between the plan and the delete and reuses c1
		if err := svc.store.Save(Run{ID: "run_new", State: RunIntake, CreatedAt: time.Now(), HeadSHA: strings.Repeat("1", 40)}); err != nil {
			t.Error(err)
		}
	}
	t.Cleanup(func() { pruneSeam = nil })
	rep, err := svc.Prune(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if !pathExists(c1) || len(rep.Checkouts) != 0 {
		t.Fatalf("c1 was deleted under a run that started after the plan: %+v", rep)
	}
}

func TestPruneKeepsRecentlyUsedCheckouts(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	c1 := filepath.Join(root, "repos", "aoagents__agent-orchestrator", strings.Repeat("1", 40))
	now := time.Now()
	if err := os.Chtimes(c1, now, now); err != nil {
		t.Fatal(err)
	}
	if rep, err := svc.Prune(ctx, 1, 1); err != nil || len(rep.Checkouts) != 0 || !pathExists(c1) {
		t.Fatalf("a checkout used a minute ago was removed: %+v %v", rep, err)
	}
	writeFile(t, filepath.Join(c1, "manifest.json"), `{"usedAt":"`+now.Add(-time.Hour).UTC().Format(time.RFC3339)+`"}`)
	if rep, err := svc.Prune(ctx, 1, 1); err != nil || len(rep.Checkouts) != 1 {
		t.Fatalf("manifest usedAt an hour ago should allow removal: %+v %v", rep, err)
	}
}

func TestPruneSkipsCheckoutsWhileARunJSONIsUnreadable(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	writeFile(t, filepath.Join(svc.store.root, "run_broken", "run.json"), "{not json")
	rep, err := svc.Prune(ctx, 1, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Checkouts) != 0 || !pathExists(filepath.Join(root, "repos", "aoagents__agent-orchestrator", strings.Repeat("1", 40))) {
		t.Fatalf("checkouts pruned with an unreadable run: %+v", rep)
	}
	if !pathExists(filepath.Join(svc.store.root, "run_broken")) || len(rep.Runs) != 2 || !strings.Contains(strings.Join(rep.Lines, "\n"), "skipped checkouts: 1 run dirs have no readable run.json (run_broken)") {
		t.Fatalf("report = %+v", rep)
	}
}

func TestPruneRemovesRunWithPermissionDeniedSubdir(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	runDir := filepath.Join(svc.store.root, "run_old1")
	blocked := filepath.Join(runDir, "scratch", "fixtures", "blocked")
	writeFile(t, filepath.Join(blocked, "nested", "locked.txt"), "fixture")
	outside := filepath.Join(t.TempDir(), "outside.txt")
	writeFile(t, outside, "keep")
	if err := os.Chmod(outside, 0o400); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(blocked, "outside")); err != nil {
		t.Fatal(err)
	}
	if err := os.Link(outside, filepath.Join(blocked, "nested", "hardlink")); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(blocked, "nested", "locked.txt"), 0); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(blocked, 0o700)
		_ = os.Chmod(filepath.Join(runDir+".pruning", "scratch", "fixtures", "blocked"), 0o700)
	})
	if err := os.Chmod(blocked, 0); err != nil {
		t.Fatal(err)
	}
	// Keep the checkouts until the next pass, after this run's files have been removed.
	if rep, err := svc.Prune(ctx, 1, 20); err != nil || len(rep.Runs) != 2 || len(rep.Checkouts) != 0 {
		t.Fatalf("first prune = %+v, %v", rep, err)
	}
	if pathExists(runDir) || pathExists(runDir+".pruning") {
		t.Fatal("run directory survived prune")
	}
	if info, err := os.Stat(outside); err != nil || info.Mode().Perm() != 0o400 {
		t.Fatalf("symlink or hard link target permissions changed: %v, %v", info, err)
	}
	if data, err := os.ReadFile(outside); err != nil || string(data) != "keep" {
		t.Fatalf("symlink target changed: %q, %v", data, err)
	}
	rep, err := svc.Prune(ctx, 1, 1)
	checkout := filepath.Join(root, "repos", "aoagents__agent-orchestrator", strings.Repeat("1", 40))
	if err != nil || len(rep.Checkouts) != 1 || pathExists(checkout) || strings.Contains(strings.Join(rep.Lines, "\n"), "skipped checkouts") {
		t.Fatalf("next checkout prune = %+v, %v", rep, err)
	}
}

func TestPruneDeletesRunsLeftAsideByAnInterruptedPass(t *testing.T) {
	for _, readable := range []bool{false, true} {
		t.Run(map[bool]string{false: "missing run.json", true: "readable run.json"}[readable], func(t *testing.T) {
			svc, _, _ := pruneFixture(t)
			aside := filepath.Join(svc.store.root, "run_x.pruning")
			if readable {
				if err := svc.store.Save(Run{ID: "run_x", State: RunRunning, HeadSHA: strings.Repeat("1", 40)}); err != nil {
					t.Fatal(err)
				}
				if err := os.Rename(filepath.Join(svc.store.root, "run_x"), aside); err != nil {
					t.Fatal(err)
				}
			}
			writeFile(t, filepath.Join(aside, "scratch", "leftover.txt"), "fixture")
			if err := svc.store.Save(Run{ID: "run_x.pruning", State: RunRunning}); err == nil {
				t.Fatal("store accepted a run id in prune's .pruning namespace")
			}
			if runs, err := svc.store.List(); err != nil || len(runs) != 4 {
				t.Fatalf("store listed an aside run: %+v, %v", runs, err)
			}
			if runs, unreadable := svc.sortedRuns(); len(runs) != 4 || len(unreadable) != 0 {
				t.Fatalf("aside run affected prune listing: %+v, %v", runs, unreadable)
			}
			if rep, err := svc.PruneDryRun(ctx, 1, 1); err != nil || !slices.Contains(rep.Runs, "runs/run_x.pruning") || !pathExists(aside) {
				t.Fatalf("dry prune = %+v, %v", rep, err)
			}
			rep, err := svc.Prune(ctx, 1, 1)
			if err != nil || len(rep.Checkouts) != 1 || len(rep.Runs) != 3 || !slices.Contains(rep.Runs, "runs/run_x.pruning") || pathExists(aside) {
				t.Fatalf("prune with aside run = %+v, %v", rep, err)
			}
		})
	}
}

func TestAutoPruneKeepsEveryRunAtKeepRunsZero(t *testing.T) {
	svc, _, recent := pruneFixture(t)
	svc.keepRuns, svc.keepCheckouts = 0, 0
	svc.autoPrune(ctx, recent)
	for id, want := range map[string]bool{"run_recent": true, "run_live": true, "run_old1": true, "run_old2": true} {
		if got := pathExists(filepath.Join(svc.store.root, id)); got != want {
			t.Errorf("%s exists = %v, want %v", id, got, want)
		}
	}
	if log, err := os.ReadFile(filepath.Join(svc.store.root, recent.ID, "comment.log")); err != nil || !strings.Contains(string(log), "pruned: ") {
		t.Fatalf("comment.log = %q, %v", log, err)
	}
}

func TestPruneKeepRunsCountsOnlyFinishedRuns(t *testing.T) {
	svc, _, _ := pruneFixture(t)
	rep, err := svc.Prune(ctx, 2, 2) // finished: recent, old2 kept; the running run does not use a slot
	if err != nil || len(rep.Runs) != 1 || rep.Runs[0] != "runs/run_old1" {
		t.Fatalf("report = %+v err=%v", rep, err)
	}
}

func TestAutoPruneIsSilentOnlyWhenNothingWasDeletedOrSkipped(t *testing.T) {
	svc, _, recent := pruneFixture(t)
	svc.keepRuns, svc.keepCheckouts = 100, 100
	logPath := filepath.Join(svc.store.root, recent.ID, "comment.log")
	svc.autoPrune(ctx, recent)
	if _, err := os.Stat(logPath); err == nil {
		t.Fatal("logged although nothing was deleted or skipped")
	}
	writeFile(t, filepath.Join(svc.store.root, "run_broken", "run.json"), "{not json")
	svc.autoPrune(ctx, recent)
	log, err := os.ReadFile(logPath)
	if err != nil || !strings.Contains(string(log), "skipped checkouts: 1 run dirs have no readable run.json (run_broken)") || !strings.Contains(string(log), "pruned: 0 checkouts, 0 runs") {
		t.Fatalf("comment.log = %q, %v", log, err)
	}
}

// pinnedMirror gives the fixture's repo a mirror whose pins are c1..c4 (the fake git lists them), and returns its path.
func pinnedMirror(t *testing.T, svc *Service, root string) (*fakeRunner, string) {
	t.Helper()
	mirror := filepath.Join(root, "mirrors", "aoagents__agent-orchestrator.git")
	writeFile(t, filepath.Join(mirror, "HEAD"), "ref: refs/heads/main\n")
	var refs []string
	for _, c := range []string{"1", "2", "3", "4"} {
		refs = append(refs, keepRefs+strings.Repeat(c, 40))
	}
	f := &fakeRunner{gitOut: map[string]string{"for-each-ref --format=%(refname)": strings.Join(refs, "\n") + "\n"}}
	svc.runner = f
	return f, mirror
}

// An intake pins its commits before its run.json names them, so while one is in flight prune drops no pin at all.
func TestPruneKeepsEveryPinWhileAnIntakeIsInFlight(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	f, _ := pinnedMirror(t, svc, root)
	svc.mu.Lock()
	done, err := svc.markBusy("run_intake", "intake")
	svc.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Prune(ctx, 1, 1); err != nil || f.count("update-ref -d") != 0 || f.count("gc") != 0 {
		t.Fatalf("a pin was dropped under an intake in flight: %v\n%q", err, f.calls)
	}
	done()
	if _, err := svc.Prune(ctx, 1, 1); err != nil || f.count("update-ref -d") != 2 || f.count("gc --prune=now --quiet") != 1 { // c1 and c4: no checkout and no unfinished run holds them
		t.Fatalf("the pins stayed after the intake ended: %v\n%q", err, f.calls)
	}
}

// A mirror in use (an intake's fetch, a prepare's clone) is skipped, with its line, and nothing in it is touched.
func TestPruneSkipsTheCleanupWhenTheMirrorIsBusy(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	f, mirror := pinnedMirror(t, svc, root)
	mu := svc.mirrorLock(mirror)
	mu.Lock()
	rep, err := svc.Prune(ctx, 1, 1)
	mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if want := "skipped mirrors/aoagents__agent-orchestrator.git: " + errMirrorBusy.Error(); !slices.Contains(rep.Lines, want) {
		t.Fatalf("lines %q lack %q", rep.Lines, want)
	}
	if n := f.count("for-each-ref") + f.count("update-ref") + f.count("gc"); n != 0 {
		t.Fatalf("a busy mirror was touched: %q", f.calls)
	}
}

// A submit's retention pass only drops the pins and marks the mirror due; the daemon's sweep runs git gc, and only when
// no verb is in flight and the mirror is free.
func TestAutoPruneOnlyUnpinsAndTheSweepCleansWhenIdle(t *testing.T) {
	svc, root, recent := pruneFixture(t)
	f, mirror := pinnedMirror(t, svc, root)
	rep, err := svc.prune(ctx, 1, 1, false, recent.ID)
	if want := "dropped 2 pinned commits from mirrors/aoagents__agent-orchestrator.git and will be cleaned when nothing is running"; err != nil || !slices.Contains(rep.Lines, want) {
		t.Fatalf("err %v lines %q", err, rep.Lines)
	}
	if f.count("gc") != 0 || !pathExists(filepath.Join(mirror, gcDueFile)) {
		t.Fatalf("submit's pass cleaned the mirror or did not mark it due: %q", f.calls)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	svc.mu.Lock()
	done, err := svc.markBusy("run_other", "prepare")
	svc.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	svc.SweepMirrors(ctx, log) // a verb is in flight
	done()
	mu := svc.mirrorLock(mirror)
	mu.Lock()
	svc.SweepMirrors(ctx, log) // the mirror is held
	mu.Unlock()
	if f.count("gc") != 0 {
		t.Fatalf("the sweep cleaned a busy daemon or mirror: %q", f.calls)
	}
	svc.SweepMirrors(ctx, log)
	svc.SweepMirrors(ctx, log) // already clean
	if n := f.count("gc --prune=now --quiet"); n != 1 || pathExists(filepath.Join(mirror, gcDueFile)) {
		t.Fatalf("git gc ran %d times, due mark left %v: %q", n, pathExists(filepath.Join(mirror, gcDueFile)), f.calls)
	}
}

// A clone that was killed leaves <name>.git.tmp; prune removes it unless the clone that owns it is still running.
func TestPruneRemovesHalfMadeMirrorsButNotOneBeingBuilt(t *testing.T) {
	svc, root, _ := pruneFixture(t)
	svc.runner = &fakeRunner{}
	mirrors := filepath.Join(root, "mirrors")
	stale, building, whole := filepath.Join(mirrors, "acme__old.git.tmp"), filepath.Join(mirrors, "acme__busy.git.tmp"), filepath.Join(mirrors, "acme__done.git")
	writeFile(t, filepath.Join(stale, "objects", "pack", "p.pack"), strings.Repeat("x", 5000))
	writeFile(t, filepath.Join(building, "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(whole, "HEAD"), "ref: refs/heads/main\n")
	busy := svc.mirrorLock(svc.store.mirrorDir("acme/busy")) // the lock a clone of acme/busy holds
	busy.Lock()
	line := "mirrors/acme__old.git.tmp (left by an interrupted clone)"
	rep, err := svc.PruneDryRun(ctx, 1, 1)
	if err != nil || !slices.Contains(rep.Lines, "would remove "+line) || !pathExists(stale) {
		t.Fatalf("dry run: err %v lines %q, stale exists %v", err, rep.Lines, pathExists(stale))
	}
	rep, err = svc.Prune(ctx, 1, 1)
	if err != nil || !slices.Contains(rep.Lines, "removed "+line) || rep.BytesFreed < 5000 {
		t.Fatalf("prune: err %v lines %q freed %d", err, rep.Lines, rep.BytesFreed)
	}
	if pathExists(stale) || !pathExists(building) || !pathExists(whole) {
		t.Fatalf("stale exists %v, the one being built exists %v, the whole mirror exists %v", pathExists(stale), pathExists(building), pathExists(whole))
	}
	busy.Unlock() // the clone is over and left its dir: the next pass takes it
	if _, err := svc.Prune(ctx, 1, 1); err != nil || pathExists(building) {
		t.Fatalf("a dir no clone owns any more stayed: %v", err)
	}
}

func TestPruneClearsClosedRunFramesEvenWhenEvidenceIsKept(t *testing.T) {
	svc, _, recent := pruneFixture(t)
	for _, id := range []string{recent.ID, "run_live"} {
		for _, leg := range []string{"head", "base"} {
			writeFile(t, filepath.Join(svc.store.root, id, leg, "frames", "000001.jpg"), "jpeg")
		}
	}
	rep, err := svc.PruneDryRun(ctx, 20, 2)
	if err != nil || !pathExists(filepath.Join(svc.store.root, recent.ID, "head", "frames")) || !strings.Contains(strings.Join(rep.Lines, "\n"), "would remove frames") {
		t.Fatalf("dry prune %+v, %v", rep, err)
	}
	if _, err := svc.Prune(ctx, 20, 2); err != nil {
		t.Fatal(err)
	}
	for _, leg := range []string{"head", "base"} {
		if pathExists(filepath.Join(svc.store.root, recent.ID, leg, "frames")) {
			t.Fatal("closed run kept frames")
		}
		if !pathExists(filepath.Join(svc.store.root, "run_live", leg, "frames")) {
			t.Fatal("open run lost frames")
		}
	}
	if !pathExists(filepath.Join(svc.store.root, recent.ID, "verdict.md")) {
		t.Fatal("prune removed retained evidence")
	}
}
