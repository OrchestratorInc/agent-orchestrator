package litmus

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Retention defaults: finished runs kept on disk, and finished runs whose checkouts are kept.
const (
	DefaultKeepRuns      = 20
	DefaultKeepCheckouts = 2
)

// PruneReport is what a retention pass removed (or, for a dry run, would remove).
type PruneReport struct {
	DryRun     bool     `json:"dryRun"`
	Checkouts  []string `json:"checkouts"` // paths relative to the litmus data dir
	Runs       []string `json:"runs"`
	BytesFreed int64    `json:"bytesFreed"`
	Lines      []string `json:"lines"` // the report as plain lines for the CLI
}

// Summary is the one-line form: "pruned: N checkouts, M runs, X GB freed".
func (r PruneReport) Summary() string {
	verb := "pruned"
	if r.DryRun {
		verb = "would prune"
	}
	return fmt.Sprintf("%s: %d checkouts, %d runs, %.1f GB freed", verb, len(r.Checkouts), len(r.Runs), float64(r.BytesFreed)/(1<<30))
}

// Prune deletes checkouts and run dirs the retention rules no longer need. It keeps every checkout an unfinished
// run (intake, prepared, running) or one of the keepCheckouts most recent finished runs references, the keepRuns
// most recent finished run dirs and every unfinished run. The evidence clone is never touched.
func (s *Service) Prune(ctx context.Context, keepRuns, keepCheckouts int) (PruneReport, error) {
	return s.prune(ctx, keepRuns, keepCheckouts, false, "")
}

// PruneDryRun reports what Prune would delete and deletes nothing.
func (s *Service) PruneDryRun(ctx context.Context, keepRuns, keepCheckouts int) (PruneReport, error) {
	return s.prune(ctx, keepRuns, keepCheckouts, true, "")
}

// recentUse is how long a checkout counts as in use after its last use: a run preparing it may not be on disk yet.
const recentUse = 10 * time.Minute

// pruneSeam, when set by a test, runs after the plan is made and before anything is deleted.
var pruneSeam func()

// pins is what the retention rules protect: checkout paths and commits of unfinished runs and of the newest
// keepCheckouts finished ones.
type pins struct{ paths, shas map[string]bool }

// checkoutPins computes the pins from runs, newest first.
func checkoutPins(runs []Run, keepCheckouts int) pins {
	p := pins{paths: map[string]bool{}, shas: map[string]bool{}}
	finished := 0
	for _, r := range runs {
		if finishedRun(r) {
			finished++
			if finished > keepCheckouts {
				continue
			}
		}
		for _, l := range r.Legs {
			if l.Worktree != "" {
				p.paths[filepath.Clean(l.Worktree)] = true
			}
		}
		for _, sha := range []string{r.HeadSHA, r.BaseSHA} {
			if sha != "" {
				p.shas[strings.ToLower(sha)] = true
			}
		}
	}
	return p
}

func (p pins) holds(dir string) bool {
	return p.paths[filepath.Clean(dir)] || shaPinned(p.shas, filepath.Base(dir))
}

// recentlyUsed reports whether the checkout was used (manifest UsedAt, else dir mtime) within recentUse.
func (s *Service) recentlyUsed(dir string) bool {
	stamp := readManifest(dir).UsedAt
	if stamp.IsZero() {
		fi, err := os.Stat(dir)
		if err != nil {
			return false
		}
		stamp = fi.ModTime()
	}
	return s.clock().Sub(stamp) < recentUse
}

// sortedRuns loads every run newest first, with the names of run dirs that could not be read. Callers hold s.mu.
func (s *Service) sortedRuns() ([]Run, []string) {
	runs, unreadable := s.loadForPrune()
	slices.SortStableFunc(runs, func(a, b Run) int { return cmp.Or(b.CreatedAt.Compare(a.CreatedAt), strings.Compare(b.ID, a.ID)) })
	return runs, unreadable
}

// prune applies the retention rules. current, when set, is a run dir that is kept whatever keepRuns says.
func (s *Service) prune(ctx context.Context, keepRuns, keepCheckouts int, dry bool, current string) (PruneReport, error) {
	rep := PruneReport{DryRun: dry, Checkouts: []string{}, Runs: []string{}, Lines: []string{}}
	if keepRuns < 0 || keepCheckouts < 0 {
		return rep, invalid("TESTING_PRUNE_INVALID", "keep-runs and keep-checkouts must not be negative")
	}
	root := filepath.Dir(s.store.root) // <dataDir>/litmus
	s.mu.Lock()
	runs, unreadable := s.sortedRuns()
	s.mu.Unlock()
	plan := checkoutPins(runs, keepCheckouts)

	// Unfinished runs are always kept; only finished runs count toward keepRuns.
	keepDir := map[string]bool{}
	if current != "" {
		keepDir[current] = true
	}
	finished := 0
	for _, r := range runs {
		if !finishedRun(r) || r.State == RunAwaitingApproval { // an awaiting run still needs its files to be posted
			keepDir[r.ID] = true
		} else {
			finished++
			if finished <= keepRuns {
				keepDir[r.ID] = true
			}
		}
	}
	for _, id := range unreadable {
		keepDir[id] = true // a run dir we cannot read may be a live one
	}
	if pruneSeam != nil {
		pruneSeam()
	}

	var errs []string
	fail := func(line string, err error) {
		errs = append(errs, err.Error())
		rep.Lines = append(rep.Lines, line)
	}
	// remove deletes path, or aside when the checkout was already moved there; both are reported as path.
	remove := func(path, aside string, into *[]string, kind string) {
		if aside == "" && ctx.Err() != nil { // a dir already moved aside is deleted whatever the context says
			return
		}
		rel, err := withinRoot(root, path)
		if err == nil && (kind == "checkout") != strings.HasPrefix(rel, "repos"+string(filepath.Separator)) {
			err = fmt.Errorf("%s is not a %s", rel, kind)
		}
		if err != nil {
			fail("skipped "+filepath.ToSlash(path)+": "+err.Error(), err)
			return
		}
		target := cmp.Or(aside, path)
		size := dirSize(target)
		verb := "removed"
		if dry {
			verb = "would remove"
		} else if err := os.RemoveAll(target); err != nil {
			fail(fmt.Sprintf("failed to remove %s: %v", filepath.ToSlash(rel), err), err)
			return
		}
		*into = append(*into, filepath.ToSlash(rel))
		rep.BytesFreed += size
		rep.Lines = append(rep.Lines, fmt.Sprintf("%s %s %s (%s)", verb, kind, filepath.ToSlash(rel), humanBytes(size)))
	}
	// removeCheckout re-reads the runs under the lock and moves the dir aside only if it is still unpinned, so a run
	// that started since the plan keeps its checkout. The move is instant; the slow delete runs unlocked.
	removeCheckout := func(d string) {
		if asideDir(d) { // moved aside by a pass that did not get to delete it: nothing can use it, delete it now
			remove(d, d, &rep.Checkouts, "checkout")
			return
		}
		if dry {
			if !s.recentlyUsed(d) {
				remove(d, "", &rep.Checkouts, "checkout")
			}
			return
		}
		if ctx.Err() != nil { // never move aside what this pass will not delete
			return
		}
		if _, err := withinRoot(root, d); err != nil { // before anything is moved
			fail("skipped "+filepath.ToSlash(d)+": "+err.Error(), err)
			return
		}
		s.mu.Lock()
		runs, unreadable := s.sortedRuns()
		aside := ""
		if len(unreadable) == 0 && !checkoutPins(runs, keepCheckouts).holds(d) && !s.recentlyUsed(d) {
			aside = d + ".pruning"
			if err := os.Rename(d, aside); err != nil {
				aside = ""
				fail(fmt.Sprintf("failed to remove %s: %v", filepath.Base(d), err), err)
			}
		}
		s.mu.Unlock()
		if aside != "" {
			remove(d, aside, &rep.Checkouts, "checkout")
		}
	}

	if len(unreadable) > 0 {
		rep.Lines = append(rep.Lines, fmt.Sprintf("skipped checkouts: %d run dirs have no readable run.json (%s), so their commits are unknown", len(unreadable), strings.Join(unreadable, ", ")))
	} else {
		repoDirs, _ := filepath.Glob(filepath.Join(root, "repos", "*", "*"))
		slices.SortFunc(repoDirs, func(a, b string) int { // dirs left aside go first, so a move never lands on one
			if x, y := asideDir(a), asideDir(b); x != y {
				return map[bool]int{true: -1, false: 1}[x]
			}
			return strings.Compare(a, b)
		})
		for _, d := range repoDirs {
			if fi, err := os.Lstat(d); err != nil || !fi.IsDir() || (plan.holds(d) && !asideDir(d)) {
				continue
			}
			removeCheckout(d)
		}
	}
	for _, r := range runs {
		if !keepDir[r.ID] {
			remove(filepath.Join(s.store.root, r.ID), "", &rep.Runs, "run")
		} else if finishedRun(r) {
			// Failed or interrupted encodes keep frames only while the run is open.
			s.mu.Lock()
			cur, err := s.store.Get(r.ID)
			closed := err == nil && finishedRun(cur)
			s.mu.Unlock()
			if closed {
				for _, leg := range []string{"head", "base"} {
					path := filepath.Join(s.store.root, r.ID, leg, "frames")
					if _, err := os.Lstat(path); err == nil {
						var frames []string
						remove(path, "", &frames, "frames")
					}
				}
			}
		}
	}
	if !dry && len(unreadable) == 0 && ctx.Err() == nil {
		mirrors, _ := filepath.Glob(filepath.Join(root, "mirrors", "*.git"))
		for _, mirror := range mirrors {
			n, err := s.unpin(ctx, mirror, func() map[string]bool { return s.neededCommits(mirror) }, current == "") // a submit's pass only unpins; the daemon's sweep cleans after
			if errors.Is(err, errMirrorBusy) {
				rep.Lines = append(rep.Lines, fmt.Sprintf("skipped mirrors/%s: %v", filepath.Base(mirror), err))
			} else if err != nil {
				fail(fmt.Sprintf("skipped mirrors/%s: %v", filepath.Base(mirror), err), err)
			} else if n > 0 {
				cleaned := map[bool]string{true: "and will be cleaned when nothing is running", false: "and cleaned it"}[current != ""]
				rep.Lines = append(rep.Lines, fmt.Sprintf("dropped %d pinned commits from mirrors/%s %s", n, filepath.Base(mirror), cleaned))
			}
		}
	}
	if ctx.Err() == nil { // a clone that was killed leaves its half-made mirror behind; a running one holds the lock
		removed, freed, err := s.sweepMirrorTmp(filepath.Join(root, "mirrors"), dry)
		for _, name := range removed {
			rep.Lines = append(rep.Lines, fmt.Sprintf("%s mirrors/%s (left by an interrupted clone)", map[bool]string{true: "would remove", false: "removed"}[dry], name))
		}
		rep.BytesFreed += freed
		if err != nil {
			fail("failed to remove a half-made mirror: "+err.Error(), err)
		}
	}
	rep.Lines = append(rep.Lines, rep.Summary())
	if len(errs) > 0 {
		return rep, fmt.Errorf("prune: %d paths could not be removed: %s", len(errs), strings.Join(errs, "; "))
	}
	return rep, nil
}

// neededCommits are the commits a mirror must keep pinned: those of its remaining checkouts (by dir name) and of every
// unfinished run of its repo. A run dir that cannot be read, or an intake still in flight (its commits are pinned
// before its run.json names them), keeps every pin ("*"). Called under the mirror lock.
func (s *Service) neededCommits(mirror string) map[string]bool {
	keep := map[string]bool{}
	s.mu.Lock()
	for k := range s.busy {
		if strings.HasSuffix(k, " intake") { // its commits are pinned but not yet in run.json
			keep["*"] = true
		}
	}
	s.mu.Unlock()
	name := strings.TrimSuffix(filepath.Base(mirror), ".git")
	dirs, _ := filepath.Glob(filepath.Join(filepath.Dir(filepath.Dir(mirror)), "repos", name, "*"))
	for _, d := range dirs {
		if !asideDir(d) {
			keep[strings.ToLower(filepath.Base(d))] = true
		}
	}
	s.mu.Lock()
	runs, unreadable := s.sortedRuns()
	s.mu.Unlock()
	for _, r := range runs {
		if (!finishedRun(r) || len(unreadable) > 0) && strings.ReplaceAll(r.Repo, "/", "__") == name {
			for _, sha := range []string{r.HeadSHA, r.BaseSHA} {
				if sha != "" {
					keep[strings.ToLower(sha)] = true
				}
			}
		}
	}
	if len(unreadable) > 0 {
		keep["*"] = true
	}
	return keep
}

// loadForPrune reads every run.json; dirs whose run.json cannot be read are returned by name so they are kept.
// A dir with no run.json yet that a tester was spawned for less than SpawnBudget ago is a run that is starting, not an
// unreadable one: it is left out of both lists, so it neither pins a commit nor stops the checkouts from being pruned.
func (s *Service) loadForPrune() (runs []Run, unreadable []string) {
	entries, _ := os.ReadDir(s.store.root)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		run, err := s.store.Get(e.Name())
		switch {
		case err == nil && run.ID == e.Name():
			runs = append(runs, run)
		case errors.Is(err, fs.ErrNotExist) && s.starting(e.Name()):
		default:
			unreadable = append(unreadable, e.Name())
		}
	}
	return runs, unreadable
}

// starting reports whether a run dir belongs to a tester spawned less than SpawnBudget ago.
func (s *Service) starting(dir string) bool {
	sid, ok := strings.CutPrefix(dir, "run_")
	if !ok {
		return false
	}
	sp, found := s.store.spawnedSession(sid)
	return found && s.clock().Sub(sp.At) < SpawnBudget
}

// finishedRun is a run that will never prepare or drive a checkout again.
func finishedRun(r Run) bool {
	switch r.State {
	case RunIntake, RunPrepared, RunRunning:
		return false
	}
	return true
}

// asideDir reports whether a checkout dir was moved aside (<sha>.pruning, possibly several times) by a pass that
// never got to delete it. No run can use such a dir, so prune deletes it without a pin check or another move.
func asideDir(dir string) bool {
	return strings.Contains(filepath.Base(dir), ".pruning")
}

// shaPinned reports whether a checkout dir (named by full SHA) is a pinned commit; a pinned SHA may be abbreviated.
func shaPinned(shas map[string]bool, name string) bool {
	name = strings.ToLower(name)
	for sha := range shas {
		if len(sha) >= 7 && strings.HasPrefix(name, sha) {
			return true
		}
	}
	return false
}

// withinRoot returns path relative to root, refusing anything that resolves outside root once symlinks in its
// parent are followed, root itself, and the evidence clone dir.
func withinRoot(root, path string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(realRoot, filepath.Join(realParent, filepath.Base(path)))
	if err != nil || !filepath.IsLocal(rel) || rel == "." {
		return "", fmt.Errorf("%s is outside the litmus data dir", path)
	}
	if first, _, _ := strings.Cut(rel, string(filepath.Separator)); first != "repos" && first != "runs" {
		return "", fmt.Errorf("%s is not under repos/ or runs/", rel)
	}
	return rel, nil
}

// dirSize sums the regular files under dir without following symlinks.
func dirSize(dir string) (n int64) {
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
			}
		}
		return nil
	})
	return n
}

func humanBytes(n int64) string {
	switch {
	case n >= 1<<30:
		return fmt.Sprintf("%.1f GB", float64(n)/(1<<30))
	case n >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(n)/(1<<20))
	case n >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(n)/(1<<10))
	}
	return fmt.Sprintf("%d B", n)
}
