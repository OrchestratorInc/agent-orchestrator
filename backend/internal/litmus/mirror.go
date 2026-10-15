package litmus

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// A mirror is Litmus's own bare copy of a repo's history: <dataDir>/litmus/mirrors/<owner>__<repo>.git, the default
// branch alone plus the PR commits fetched into it by id. Every build copy is a shared clone of it, so Litmus never
// depends on a checkout of the user's, and every commit a copy uses is pinned under refs/litmus/keep/ so that no
// cleanup can delete it from under the copy. Git's own cleanup is off in a mirror; only prune cleans it.
const keepRefs = "refs/litmus/keep/"

// ghCredentials lets git read a private repo with the user's gh login, without writing to their git config.
const ghCredentials = "credential.helper=!gh auth git-credential" // #nosec G101 -- a helper command, not a secret

// mirrorDir is where the repo's mirror lives.
func (s *Store) mirrorDir(repo string) string {
	return filepath.Join(filepath.Dir(s.root), "mirrors", strings.ReplaceAll(repo, "/", "__")+".git")
}

// mirrorLock serialises the work on one mirror: a fetch and its pin, a clone from it, or prune's cleanup never
// interleave. Callers do not hold s.mu.
func (s *Service) mirrorLock(mirror string) *sync.Mutex {
	s.mu.Lock()
	defer s.mu.Unlock()
	mu, ok := s.mirrorMu[mirror]
	if !ok {
		mu = &sync.Mutex{}
		s.mirrorMu[mirror] = mu
	}
	return mu
}

// ensureMirror makes the repo's mirror when it is missing: a bare clone of the default branch alone (the repo's other
// branches and pull refs are not wanted), with git's cleanup off. Callers hold the mirror lock.
func (s *Service) ensureMirror(ctx context.Context, repo string) error {
	dir := s.store.mirrorDir(repo)
	if fileExists(filepath.Join(dir, "HEAD")) {
		return nil
	}
	tmp := dir + ".tmp" // a mirror exists only once it is whole and configured; a crash mid-clone leaves this behind
	_ = os.RemoveAll(dir)
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(filepath.Dir(dir), 0o750); err != nil {
		return err
	}
	_, err := s.git(ctx, filepath.Dir(dir), "-c", ghCredentials, "clone", "--bare", "--single-branch", "https://github.com/"+repo+".git", tmp)
	if err == nil {
		_, err = s.git(ctx, tmp, "config", "gc.auto", "0")
	}
	if err == nil {
		err = os.Rename(tmp, dir)
	}
	if err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

// fetchInto brings refs (commit ids or ref names) from GitHub into the mirror, once more when the first try fails.
// Callers hold the mirror lock and pin what they fetched.
func (s *Service) fetchInto(ctx context.Context, mirror string, refs ...string) error {
	args := append([]string{"-c", ghCredentials, "fetch", "--no-tags", "origin"}, refs...)
	_, err := s.git(ctx, mirror, args...)
	if err != nil {
		_, err = s.git(ctx, mirror, args...)
	}
	return err
}

// pin names each commit refs/litmus/keep/<id> in the mirror, so no cleanup can delete it while a build copy needs it.
func (s *Service) pin(ctx context.Context, mirror string, ids ...string) error {
	for _, id := range ids {
		if _, err := s.git(ctx, mirror, "update-ref", keepRefs+id, id); err != nil {
			return err
		}
	}
	return nil
}

// borrowsFrom reports whether the shared clone at dir takes its history from mirror: its alternates file names the
// mirror's objects dir. A copy borrowing from anywhere else (a checkout of the user's, say) is remade.
func borrowsFrom(dir, mirror string) bool {
	data, err := os.ReadFile(filepath.Join(dir, ".git", "objects", "info", "alternates"))
	if err != nil {
		return false
	}
	want := filepath.Join(mirror, "objects")
	for _, l := range strings.Split(string(data), "\n") {
		if l = strings.TrimSpace(l); l == want || samePath(l, want) {
			return true
		}
	}
	return false
}

// samePath reports whether two paths name the same directory once symlinks are followed.
func samePath(a, b string) bool {
	fa, err := os.Stat(a)
	if err != nil {
		return false
	}
	fb, err := os.Stat(b)
	return err == nil && os.SameFile(fa, fb)
}

// errMirrorBusy is unpin's answer when another verb (an intake's fetch, a prepare's clone) holds the mirror: the
// retention pass runs inline in submit and must not wait behind a cold clone; the pins go at a later pass.
var errMirrorBusy = errors.New("another run is using the mirror; its pins will be dropped by a later prune")

// gcBudget is how long the daemon's sweep lets one mirror cleanup take.
const gcBudget = 10 * time.Minute

// gcDueFile, inside a mirror, says pins were dropped from it since its last cleanup: the daemon's idle sweep cleans it.
const gcDueFile = "litmus-gc-due"

// unpin drops the mirror's pins that keep() does not list. With gc set it then cleans the mirror inline when any went
// (git gc, which takes seconds on a big mirror), as an explicit prune does; without it the mirror is marked as due and
// the daemon's sweep cleans it when nothing is running (submit's pass must not hold its caller up). keep runs under the
// mirror lock, so a copy made in the meantime cannot lose its commit. Returns how many pins were dropped.
func (s *Service) unpin(ctx context.Context, mirror string, keep func() map[string]bool, gc bool) (int, error) {
	mu := s.mirrorLock(mirror)
	if !mu.TryLock() {
		return 0, errMirrorBusy
	}
	defer mu.Unlock()
	out, err := s.git(ctx, mirror, "for-each-ref", "--format=%(refname)", keepRefs)
	if err != nil {
		return 0, err
	}
	needed, dropped := keep(), 0
	if needed["*"] { // a run dir that cannot be read may need any commit: drop nothing
		return 0, nil
	}
	for _, ref := range strings.Fields(out) {
		if sha := strings.TrimPrefix(ref, keepRefs); !needed[strings.ToLower(sha)] {
			if _, err := s.git(ctx, mirror, "update-ref", "-d", ref); err != nil {
				return dropped, err
			}
			dropped++
		}
	}
	if dropped == 0 {
		return 0, nil
	}
	if err := os.WriteFile(filepath.Join(mirror, gcDueFile), nil, 0o600); err != nil {
		return dropped, err
	}
	if !gc {
		return dropped, nil
	}
	return dropped, s.gcLocked(ctx, mirror)
}

// gcLocked cleans the mirror and clears its due mark. Callers hold the mirror lock.
func (s *Service) gcLocked(ctx context.Context, mirror string) error {
	if _, err := s.git(ctx, mirror, "gc", "--prune=now", "--quiet"); err != nil {
		return err
	}
	return os.Remove(filepath.Join(mirror, gcDueFile))
}

// SweepMirrors cleans the mirrors that were marked due by a pass that dropped pins, but only while no verb of any run is
// in flight, and skips a mirror another verb holds. Each cleanup has gcBudget; a failure is logged and the mirror stays
// due. The daemon calls it on its sweep tick.
func (s *Service) SweepMirrors(ctx context.Context, log *slog.Logger) {
	mirrors, _ := filepath.Glob(filepath.Join(filepath.Dir(s.store.root), "mirrors", "*.git"))
	for _, mirror := range mirrors {
		if !fileExists(filepath.Join(mirror, gcDueFile)) || s.anyInFlight() {
			continue
		}
		mu := s.mirrorLock(mirror)
		if !mu.TryLock() {
			continue
		}
		gcCtx, cancel := context.WithTimeout(ctx, gcBudget)
		start := time.Now()
		if err := s.gcLocked(gcCtx, mirror); err != nil {
			log.Warn("litmus: mirror cleanup failed", "mirror", filepath.Base(mirror), "error", gitReason(err))
		} else {
			log.Debug("litmus: mirror cleanup finished", "mirror", filepath.Base(mirror), "took", time.Since(start))
		}
		cancel()
		mu.Unlock()
	}
}

// sweepMirrorTmp removes the half-made mirrors a killed clone left behind (<name>.git.tmp in the mirrors dir) unless the
// clone is still running, which holds that mirror's lock. Returns what it removed (names), the bytes freed and the
// failures. A dry run only reports.
func (s *Service) sweepMirrorTmp(mirrorsDir string, dry bool) (removed []string, freed int64, err error) {
	tmps, _ := filepath.Glob(filepath.Join(mirrorsDir, "*.git.tmp"))
	for _, tmp := range tmps {
		mu := s.mirrorLock(strings.TrimSuffix(tmp, ".tmp"))
		if !mu.TryLock() { // a clone is building it right now
			continue
		}
		size := dirSize(tmp)
		if !dry {
			if rerr := os.RemoveAll(tmp); rerr != nil {
				err = errors.Join(err, rerr)
				mu.Unlock()
				continue
			}
		}
		mu.Unlock()
		removed, freed = append(removed, filepath.Base(tmp)), freed+size
	}
	return removed, freed, err
}
