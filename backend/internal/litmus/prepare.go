package litmus

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// MatchRepo reports whether a repository URL or git remote names owner/repo on GitHub, ignoring case.
func MatchRepo(rawURL, owner, repo string) bool {
	id, err := domain.ParseRepositoryIdentity(rawURL)
	return err == nil && id.Provider == "github" && strings.EqualFold(id.Namespace, owner) && strings.EqualFold(id.Name, repo)
}

// RepoResolver returns the local checkout of a repo that clones are made from. names are
// owner/repo spellings of the same repository (as gh resolved it, as the user wrote it).
// A failure to find one carries the message the worker sees.
type RepoResolver func(ctx context.Context, names ...string) (string, error)

// localCheckout checks that names resolve to a registered project's checkout, else TESTING_REPO_UNRESOLVED. The
// registration is a gate (Litmus tests the repos the user added to AO), not where the code comes from: that is the mirror.
func (s *Service) localCheckout(ctx context.Context, names ...string) error {
	names = slices.Compact(slices.DeleteFunc(slices.Clone(names), func(n string) bool { return n == "" }))
	err := errors.New("no project registry is wired")
	if s.resolve != nil {
		var local string
		if local, err = s.resolve(ctx, names...); err == nil && local == "" {
			err = fmt.Errorf("no registered AO project matches %s; add the checkout with `ao project add`", strings.Join(names, " or "))
		}
	}
	if err != nil {
		return apierr.NotFound("TESTING_REPO_UNRESOLVED", err.Error())
	}
	return nil
}

// Runner runs a command to completion and returns its combined output; a nil env
// inherits the daemon's. Tests swap it so no git or npm ever runs.
type Runner interface {
	Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error)
}

// stdoutRunner is a Runner that can keep stdout apart from stderr, for git output that is parsed.
type stdoutRunner interface {
	RunOut(ctx context.Context, dir string, env []string, name string, args ...string) (stdout []byte, err error)
}

// cappedRunner is a Runner that can keep the first limit bytes of stdout and stop the command there, for output that may
// be huge when its start is enough (a patch). cut says the command had more to say.
type cappedRunner interface {
	RunCapped(ctx context.Context, dir string, env []string, limit int, name string, args ...string) (stdout []byte, cut bool, err error)
}

type execRunner struct{}

func (execRunner) Run(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := process.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, env
	out, err := cmd.CombinedOutput()
	if err != nil {
		err = fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, lastLines(out, 20))
	}
	return out, err
}

// RunOut returns stdout alone; stderr only goes into the error, so a git warning never lands in parsed output.
func (execRunner) RunOut(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	cmd := process.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env = dir, env
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	if err != nil {
		detail := stderr.Bytes()
		if len(bytes.TrimSpace(detail)) == 0 {
			detail = stdout.Bytes()
		}
		err = fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, lastLines(detail, 20))
	}
	return stdout.Bytes(), err
}

// RunCapped keeps the first limit bytes of stdout. A command that writes more is stopped there and that is cut, not a
// failure: memory stays bounded by limit whatever the command would have printed.
func (execRunner) RunCapped(ctx context.Context, dir string, env []string, limit int, name string, args ...string) ([]byte, bool, error) {
	ctx, stop := context.WithCancel(ctx)
	defer stop()
	cmd := process.CommandContext(ctx, name, args...)
	cmd.Dir, cmd.Env, cmd.WaitDelay = dir, env, 2*time.Second
	out, stderr := &cutWriter{limit: limit, stop: stop}, &capWriter{max: 64 << 10}
	cmd.Stdout, cmd.Stderr = out, stderr
	err := cmd.Run()
	if out.cut {
		return out.buf, true, nil
	}
	if err != nil {
		err = fmt.Errorf("%s %s: %w\n%s", name, strings.Join(args, " "), err, lastLines(stderr.buf, 20))
	}
	return out.buf, false, err
}

// cutWriter keeps the first limit bytes written to it. The write that goes past limit calls stop, which kills the
// command, and is refused, as is every write after it.
type cutWriter struct {
	buf   []byte
	limit int
	cut   bool
	stop  context.CancelFunc
}

var errOutputCut = errors.New("output cut")

func (w *cutWriter) Write(p []byte) (int, error) {
	if w.cut {
		return 0, errOutputCut
	}
	room := w.limit - len(w.buf)
	if len(p) <= room {
		w.buf = append(w.buf, p...)
		return len(p), nil
	}
	w.buf, w.cut = append(w.buf, p[:room]...), true
	w.stop()
	return room, errOutputCut
}

func lastLines(b []byte, n int) string {
	lines := strings.Split(strings.TrimRight(string(b), "\n"), "\n")
	return strings.Join(lines[max(0, len(lines)-n):], "\n")
}

// baseEnv is the daemon's environment minus anything that could steer a target
// at the real AO (AO_*) or make Electron behave as plain node, with nodeDir first on PATH.
func baseEnv(nodeDir string) []string {
	var env []string
	for _, kv := range os.Environ() {
		switch {
		case strings.HasPrefix(kv, "AO_"), strings.HasPrefix(kv, "ELECTRON_RUN_AS_NODE="):
			continue
		case strings.HasPrefix(kv, "PATH=") && nodeDir != "":
			kv = "PATH=" + nodeDir + string(os.PathListSeparator) + strings.TrimPrefix(kv, "PATH=")
		}
		env = append(env, kv)
	}
	return append(env, "GIT_TERMINAL_PROMPT=0")
}

func defaultNodeDirs(major string) []string {
	home, _ := os.UserHomeDir()
	return []string{"/usr/local/bin", "/opt/homebrew/bin", filepath.Join(home, ".nvm", "versions", "node", "v"+major+"*", "bin"), filepath.Join(home, ".hermes", "node", "bin")}
}

// probeNode finds a node binary of the pinned major version; no pin means the daemon's PATH decides.
func (s *Service) probeNode(ctx context.Context, major string) (path, version string, err error) {
	if major == "" {
		return "", "", nil
	}
	for _, pattern := range s.nodeDirs(major) {
		dirs, _ := filepath.Glob(pattern)
		for _, d := range dirs {
			bin := filepath.Join(d, "node")
			if _, err := os.Stat(bin); err != nil {
				continue
			}
			if out, err := s.runner.Run(ctx, "", nil, bin, "--version"); err == nil && strings.HasPrefix(strings.TrimSpace(string(out)), "v"+major+".") {
				return bin, strings.TrimSpace(string(out)), nil
			}
		}
	}
	return "", "", apierr.Unavailable("TESTING_NODE_MISSING", "no Node "+major+" found in /usr/local/bin, /opt/homebrew/bin, ~/.nvm or ~/.hermes; install it")
}

// Manifest is manifest.json in a clone dir: what was built there and how long it took.
// InstalledAt is the complete marker other clones copy node_modules from.
type Manifest struct {
	SHA         string             `json:"sha"`
	LockHash    string             `json:"lockHash"`
	NodeVersion string             `json:"nodeVersion,omitempty"`
	NodePath    string             `json:"nodePath,omitempty"`
	Install     string             `json:"install,omitempty"` // warm | copied from <clone> | npm ci
	InstalledAt time.Time          `json:"installedAt,omitzero"`
	PredevAt    time.Time          `json:"predevAt,omitzero"`
	UsedAt      time.Time          `json:"usedAt,omitzero"`
	Durations   map[string]float64 `json:"durations,omitempty"` // seconds, of the steps that actually ran
}

func manifestPath(dir string) string { return filepath.Join(dir, "manifest.json") }

func readManifest(dir string) (m Manifest) {
	if data, err := os.ReadFile(manifestPath(dir)); err == nil {
		_ = json.Unmarshal(data, &m)
	}
	if m.Durations == nil {
		m.Durations = map[string]float64{}
	}
	return m
}

func writeManifest(dir string, m Manifest) error {
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(manifestPath(dir), data)
}

var shaPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

func (s *Service) git(ctx context.Context, dir string, args ...string) (string, error) {
	out, err := s.gitRaw(ctx, dir, args...)
	return strings.TrimSpace(out), err
}

// gitRaw is git's output untrimmed, and stdout only when the runner can separate it, for output that is parsed.
func (s *Service) gitRaw(ctx context.Context, dir string, args ...string) (string, error) {
	full := append([]string{"-C", dir}, args...)
	var out []byte
	var err error
	if r, ok := s.runner.(stdoutRunner); ok {
		out, err = r.RunOut(ctx, "", baseEnv(""), "git", full...)
	} else {
		out, err = s.runner.Run(ctx, "", baseEnv(""), "git", full...)
	}
	return string(out), err
}

// gitCapped is gitRaw for output that may be huge: at most limit bytes of stdout; cut says git had more and was
// stopped. A runner that cannot stop the command (the tests' fake) returns it whole and it is cut here.
func (s *Service) gitCapped(ctx context.Context, dir string, limit int, args ...string) (string, bool, error) {
	if r, ok := s.runner.(cappedRunner); ok {
		out, cut, err := r.RunCapped(ctx, "", baseEnv(""), limit, "git", append([]string{"-C", dir}, args...)...)
		return string(out), cut, err
	}
	out, err := s.gitRaw(ctx, dir, args...)
	return out[:min(len(out), limit)], len(out) > limit, err
}

func (s *Service) sh(ctx context.Context, dir string, env []string, script string) ([]byte, error) {
	return s.runner.Run(ctx, dir, env, "sh", "-c", script)
}

// prepareLeg makes <dataDir>/litmus/repos/<owner>__<repo>/<sha> ready to launch: a
// shared clone of the repo's mirror (never a worktree, so sessions inside the private AO
// cannot register worktrees in the mirror), node_modules reused or installed, predev run.
// The registered AO project is only the gate: Litmus tests the repos the user added.
func (s *Service) prepareLeg(ctx context.Context, repo string, r Recipe, leg, sha string, aka ...string) (Leg, error) {
	if !shaPattern.MatchString(sha) {
		return Leg{}, invalid("TESTING_SHA_INVALID", "not a commit SHA: "+sha)
	}
	if err := s.localCheckout(ctx, append([]string{repo}, aka...)...); err != nil {
		return Leg{}, err
	}
	nodeBin, nodeVersion, err := s.probeNode(ctx, r.Toolchain.Node)
	if err != nil {
		return Leg{}, err
	}
	full, err := s.resolveSHA(ctx, repo, sha)
	if err != nil {
		return Leg{}, err
	}
	dir := filepath.Join(s.store.reposDir(repo), full)
	lock := s.checkoutLock(dir)
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return Leg{}, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return Leg{}, err
	}
	m, err := s.cloneLeg(ctx, repo, dir, full)
	if err != nil {
		return Leg{}, err
	}
	took := func(step string, since time.Time) {
		m.Durations[step] = time.Since(since).Round(time.Millisecond).Seconds()
	}
	m.SHA, m.NodePath, m.NodeVersion, m.UsedAt = full, nodeBin, nodeVersion, s.clock()
	env := baseEnv("")
	if nodeBin != "" {
		env = baseEnv(filepath.Dir(nodeBin))
	}

	if r.Install.Cmd != "" {
		hash, err := lockHash(dir, r.Install.CacheKey, r.Toolchain.Node)
		if err != nil {
			return Leg{}, err
		}
		t := time.Now()
		if err := s.install(ctx, r, dir, hash, env, &m); err != nil {
			return Leg{}, err
		}
		if m.Install != "warm" {
			took("install", t)
		}
		if err := writeManifest(dir, m); err != nil {
			return Leg{}, err
		}
	}
	if r.Build.Cmd != "" && m.PredevAt.IsZero() {
		t := time.Now()
		if _, err := s.sh(ctx, filepath.Join(dir, r.Build.Dir), env, r.Build.Cmd); err != nil {
			return Leg{}, err
		}
		m.PredevAt = s.clock()
		took("predev", t)
	}
	return Leg{Name: leg, Worktree: dir, BuildManifest: manifestPath(dir), State: "prepared"}, writeManifest(dir, m)
}

// checkoutLock guards checkout, install, build and manifest writes for one folder. Waiters can cancel.
func (s *Service) checkoutLock(dir string) chan struct{} {
	s.mu.Lock()
	defer s.mu.Unlock()
	lock, ok := s.checkoutMu[dir]
	if !ok {
		lock = make(chan struct{}, 1)
		s.checkoutMu[dir] = lock
	}
	return lock
}

// resolveSHA resolves and pins a full commit id before choosing its checkout lock. It releases the mirror lock before waiting.
func (s *Service) resolveSHA(ctx context.Context, repo, sha string) (full string, err error) {
	mirror := s.store.mirrorDir(repo)
	mu := s.mirrorLock(mirror)
	mu.Lock()
	defer mu.Unlock()
	if err := s.ensureMirror(ctx, repo); err != nil {
		return "", fmt.Errorf("make the mirror of %s: %w", repo, err)
	}
	rev := func() (string, error) { return s.git(ctx, mirror, "rev-parse", "--verify", "--quiet", sha+"^{commit}") }
	full, err = rev()
	if err != nil { // the mirror may predate the PR's commits
		if ferr := s.fetchInto(ctx, mirror, sha); ferr == nil {
			full, err = rev()
		}
	}
	if err != nil {
		return "", apierr.Unavailable("TESTING_SHA_MISSING", fmt.Sprintf("commit %s is not in the mirror of %s and could not be fetched", sha, repo))
	}
	if err := s.pin(ctx, mirror, full); err != nil {
		return "", err
	}
	return full, nil
}

// cloneLeg leaves a shared clone of the mirror at dir, checked out at full.
// A copy that is absent, interrupted, at another commit or borrowing its history from elsewhere is remade.
// Callers hold the checkout lock; the mirror lock covers only the git work.
func (s *Service) cloneLeg(ctx context.Context, repo, dir, full string) (m Manifest, err error) {
	mirror := s.store.mirrorDir(repo)
	mu := s.mirrorLock(mirror)
	mu.Lock()
	defer mu.Unlock()
	m = readManifest(dir)
	t := time.Now()
	_, gitErr := os.Stat(filepath.Join(dir, ".git"))
	if head, err := s.git(ctx, dir, "rev-parse", "HEAD"); gitErr != nil || err != nil || head != full || !borrowsFrom(dir, mirror) {
		_ = os.RemoveAll(dir) // absent, an interrupted clone, or a copy borrowing from a checkout of the user's
		m = Manifest{Durations: map[string]float64{}}
		if _, err := s.runner.Run(ctx, "", baseEnv(""), "git", "clone", "--shared", "--no-checkout", mirror, dir); err != nil {
			return m, err
		}
		if _, err := s.git(ctx, dir, "checkout", "--detach", full); err != nil {
			return m, err
		}
		m.Durations["clone"] = time.Since(t).Round(time.Millisecond).Seconds()
	}
	return m, nil
}

// pruneClone forgets the worktrees of a shared clone whose directories are gone and deletes the ao/litmus-*
// branches no remaining worktree has checked out. The private AO that ran inside an earlier leg registered
// both in this clone; left behind they make a later spawn fail with BRANCH_CHECKED_OUT_ELSEWHERE.
func (s *Service) pruneClone(ctx context.Context, dir string) error {
	if dir == "" {
		return nil
	}
	if !statable(filepath.Join(dir, ".git")) {
		return nil // the clone itself is gone (see Prune)
	}
	if _, err := s.git(ctx, dir, "worktree", "prune"); err != nil {
		return fmt.Errorf("prune worktrees of %s: %w", filepath.Base(dir), err)
	}
	listed, err := s.git(ctx, dir, "worktree", "list", "--porcelain")
	if err != nil {
		return fmt.Errorf("list worktrees of %s: %w", filepath.Base(dir), err)
	}
	branches, err := s.git(ctx, dir, "for-each-ref", "--format=%(refname:short)", "refs/heads/ao/litmus-*")
	if err != nil {
		return fmt.Errorf("list branches of %s: %w", filepath.Base(dir), err)
	}
	checkedOut := map[string]bool{}
	for _, line := range strings.Split(listed, "\n") {
		if b, ok := strings.CutPrefix(strings.TrimSpace(line), "branch refs/heads/"); ok {
			checkedOut[b] = true
		}
	}
	var stale []string
	for _, b := range strings.Fields(branches) {
		if !checkedOut[b] {
			stale = append(stale, b)
		}
	}
	if len(stale) == 0 {
		return nil
	}
	if _, err := s.git(ctx, dir, append([]string{"branch", "-D"}, stale...)...); err != nil {
		return fmt.Errorf("delete branches of %s: %w", filepath.Base(dir), err)
	}
	return nil
}

// lockHash keys the node_modules cache: the cacheKey files' contents plus the node major.
func lockHash(dir string, keys []string, major string) (string, error) {
	h := sha256.New()
	for _, k := range keys {
		data, err := os.ReadFile(filepath.Join(dir, k))
		if err != nil {
			return "", fmt.Errorf("recipe cacheKey: %w", err)
		}
		h.Write(data)
	}
	return fmt.Sprintf("%x-node%s", h.Sum(nil), major), nil
}

// install leaves a complete node_modules in dir: already there (warm), copy-on-write
// copied from a sibling clone with the same lock hash, or a fresh install.
func (s *Service) install(ctx context.Context, r Recipe, dir, hash string, env []string, m *Manifest) error {
	modules := filepath.Join(dir, r.Install.Dir, "node_modules")
	if _, err := os.Stat(modules); err == nil && m.LockHash == hash && !m.InstalledAt.IsZero() {
		m.Install = "warm"
		return nil
	}
	m.LockHash, m.PredevAt = hash, time.Time{}
	if donor := findDonor(filepath.Dir(dir), dir, hash, r.Install.Dir); donor != "" {
		for _, flags := range [][]string{{"-c", "-R"}, {"-R"}} { // -c clones on APFS; plain copy elsewhere
			_ = os.RemoveAll(modules)
			if _, err := s.runner.Run(ctx, "", nil, "cp", append(flags, filepath.Join(donor, r.Install.Dir, "node_modules"), modules)...); err == nil {
				m.Install, m.InstalledAt = "copied from "+filepath.Base(donor), s.clock()
				return nil
			}
		}
		_ = os.RemoveAll(modules)
	}
	if _, err := s.sh(ctx, filepath.Join(dir, r.Install.Dir), env, r.Install.Cmd); err != nil {
		return err
	}
	m.Install, m.InstalledAt = "npm ci", s.clock()
	return nil
}

// findDonor returns a sibling clone whose node_modules is complete and has the same hash.
func findDonor(reposDir, self, hash, installDir string) string {
	entries, _ := os.ReadDir(reposDir)
	for _, e := range entries {
		d := filepath.Join(reposDir, e.Name())
		if m := readManifest(d); d != self && m.LockHash == hash && !m.InstalledAt.IsZero() {
			if fi, err := os.Stat(filepath.Join(d, installDir, "node_modules")); err == nil && fi.IsDir() {
				return d
			}
		}
	}
	return ""
}

// Prepare clones and builds the head, then the base, sequentially. A second call
// while one is building (a retry after a client timeout) waits for that build and
// then runs itself, which is quick once the clones are warm.
func (s *Service) Prepare(ctx context.Context, sessionID string) (Run, error) {
	for {
		run, done, err := s.begin(sessionID, "prepare")
		if err == nil {
			return s.prepare(ctx, run, done)
		}
		ch, on := s.running(sessionID, "prepare")
		if !on {
			return Run{}, err
		}
		select {
		case <-ch:
		case <-ctx.Done():
			return Run{}, ctx.Err()
		}
	}
}

func (s *Service) prepare(ctx context.Context, run Run, done func()) (Run, error) {
	defer done()
	if run.HeadSHA == "" || run.BaseSHA == "" || (run.State != RunIntake && run.State != RunPrepared) {
		return Run{}, conflict("TESTING_RUN_STATE", fmt.Sprintf("run %s is %s with head %q; prepare needs a pinned run that is not launched yet", run.ID, run.State, run.HeadSHA))
	}
	recipe, err := s.store.LoadRecipe(run.Repo)
	if err != nil {
		return Run{}, err
	}
	detached := context.WithoutCancel(ctx) // a CLI timeout must not kill an install halfway
	ctx, cancel := context.WithTimeout(detached, s.prepareBudget)
	defer cancel()
	var legs []Leg
	for _, l := range []struct{ name, sha string }{{"head", run.HeadSHA}, {"base", run.BaseSHA}} {
		leg, err := s.prepareLeg(ctx, run.Repo, recipe, l.name, l.sha, run.RequestedRepo)
		if err != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) { // stuck: the budget ran out inside this leg
				ready := make([]string, len(legs))
				for i, done := range legs {
					ready[i] = done.Name
				}
				return Run{}, s.blockStuckPrepare(detached, run, l.name, ready)
			}
			var typed *apierr.Error
			if !errors.As(err, &typed) { // a git, install or build failure: the tester gets the last lines, not a bare 500
				slog.Warn("litmus: prepare failed", "run", run.ID, "leg", l.name, "sha", l.sha, "error", err)
				err = apierr.Unavailable("TESTING_PREPARE_FAILED", fmt.Sprintf("%s build at %.8s failed: %v", l.name, l.sha, err))
			}
			return Run{}, err
		}
		if err := s.pruneClone(ctx, leg.Worktree); err != nil { // best effort: a stale record only matters if a spawn trips on it
			slog.Warn("litmus: could not prune the shared clone", "run", run.ID, "leg", l.name, "error", err)
		}
		legs = append(legs, leg)
	}
	return s.update(run.ID, func(cur *Run) {
		if cur.State == RunIntake { // the tester's time starts when the builds are ready; a repeat prepare does not extend it
			cur.Deadline = s.clock().Add(RunBudget)
		}
		cur.Legs, cur.State, cur.Error = legs, RunPrepared, ""
	})
}

// blockStuckPrepare closes a run whose prepare ran past s.prepareBudget as blocked, the way the tester's own
// `submit --blocked` would, and returns the error prepare answers with. ready names the builds that did finish, for the
// comment. A submit already in flight closes the run itself.
func (s *Service) blockStuckPrepare(ctx context.Context, run Run, leg string, ready []string) error {
	reason := fmt.Sprintf("the builds were not ready after %d minutes (the %s build was still going)", int(s.prepareBudget.Minutes()), leg)
	slog.Warn("litmus: prepare ran past its budget", "run", run.ID, "leg", leg, "budget", s.prepareBudget)
	s.mu.Lock()
	done, err := s.markBusy(run.ID, "submit")
	s.mu.Unlock()
	if err == nil {
		defer done()
		var checks []Check
		if run, err = s.update(run.ID, func(cur *Run) { cur.Ready = ready }); err == nil {
			if checks, err = s.store.Checks(run.ID); err == nil {
				_, err = s.closeRun(ctx, run, checks, "", reason, "")
			}
		}
	}
	if err != nil {
		slog.Warn("litmus: could not close a run whose prepare ran too long", "run", run.ID, "error", err)
	}
	return apierr.Unavailable("TESTING_PREPARE_TIMEOUT", "prepare: "+reason+"; the run is closed as blocked")
}

// statable reports whether p can be stat-ed, whatever it is.
func statable(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
