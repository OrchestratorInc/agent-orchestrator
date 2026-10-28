package litmus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Service owns the run lifecycle. Every verb is keyed by the calling session.
type Service struct {
	mu              sync.Mutex
	store           *Store
	clock           func() time.Time
	gh              GH
	resolve         RepoResolver
	runner          Runner
	sys             sysOps
	nodeDirs        func(major string) []string
	closeEngine     func(runID string)  // cleanup calls it before killing anything
	busy            map[string]busyVerb // long verbs in flight, by run and verb
	engines         map[string]*Engine  // by run/leg
	recorderMu      sync.Mutex
	recorders       map[string]*screencast // by run/leg, separate from action locks
	seqs            map[string]int         // receipts handed out, by run
	engExec         execFn
	media           Media                                              // ffmpeg, behind an interface so tests never run it
	spawn           Spawner                                            // launches testing workers; nil until the daemon wires it
	report          Reporter                                           // posts the session's `done` report after a submit; nil until the daemon wires it
	kill            Killer                                             // stops the tester's session after a submit; nil until the daemon wires it
	activity        SessionActivity                                    // reads a tester's activity for the sweep; nil until the daemon wires it
	binary          string                                             // the agent-browser binary; empty when it was not found (reported on first use)
	login           string                                             // the gh token's login, read by the first comment post
	evidenceMu      sync.Mutex                                         // one publish at a time: runs of a repo share its evidence clone
	prepareBudget   time.Duration                                      // longest a `prepare` verb may run; tests shorten it
	keepRuns        int                                                // retention after every run: finished run dirs kept
	keepCheckouts   int                                                // retention after every run: finished runs whose checkouts are kept
	spawnMarkerWait time.Duration                                      // longest intake waits for Start to finish saving a spawned session's modes
	projectRepo     func(ctx context.Context, projectID string) string // the project's own owner/repo; nil reads none
	projectHarness  func(ctx context.Context, projectID string) string // the project's worker harness; nil reads none

	mirrorMu   map[string]*sync.Mutex   // one at a time per mirror: a fetch and its pin, a clone, or prune's cleanup
	checkoutMu map[string]chan struct{} // one preparation at a time per checkout
	retryPause time.Duration            // before intake asks gh again about a PR it did not find; tests zero it
}

// New builds a service over store. A nil clock uses time.Now.
func New(store *Store, clock func() time.Time) *Service {
	if clock == nil {
		clock = time.Now
	}
	s := &Service{store: store, clock: clock, gh: execGH{}, runner: execRunner{}, sys: realSys(), nodeDirs: defaultNodeDirs, busy: map[string]busyVerb{},
		engines: map[string]*Engine{}, seqs: map[string]int{}, engExec: realExec, spawnMarkerWait: 10 * time.Second,
		media: execMedia{}, prepareBudget: PrepareBudget, keepRuns: DefaultKeepRuns, keepCheckouts: DefaultKeepCheckouts, mirrorMu: map[string]*sync.Mutex{}, checkoutMu: map[string]chan struct{}{}, retryPause: 2 * time.Second}
	s.closeEngine = s.closeEngines
	if exe, err := os.Executable(); err == nil {
		s.binary, _ = resolveBinary("", exe)
	}
	return s
}

// WithBrowserBinary selects the host's agent-browser executable.
func (s *Service) WithBrowserBinary(path string) *Service {
	if exe, err := os.Executable(); err == nil {
		s.binary, _ = resolveBinary(path, exe)
	}
	return s
}

// autoPrune is the retention pass a closed run ends with. It writes the summary, and any skip lines, to the run's
// comment.log when something was deleted or skipped, and never fails the run.
func (s *Service) autoPrune(ctx context.Context, run Run) {
	ctx = context.WithoutCancel(ctx)                                     // the client that closed the run may hang up; the disk is freed all the same
	rep, err := s.prune(ctx, s.keepRuns, s.keepCheckouts, false, run.ID) // this run's dir is kept whatever keepRuns says
	if err != nil {
		s.commentLogf(run, "prune failed: %v", err)
		return
	}
	skips := slices.DeleteFunc(slices.Clone(rep.Lines), func(l string) bool { return !strings.HasPrefix(l, "skipped") })
	if len(rep.Checkouts)+len(rep.Runs) == 0 && len(skips) == 0 {
		return // nothing deleted and nothing skipped: nothing to say
	}
	for _, l := range skips { // a skipped path or checkout is why less was deleted than expected
		s.commentLogf(run, "%s", l)
	}
	s.commentLogf(run, "%s", rep.Summary())
}

// WithGH swaps the gh runner, so tests never reach the network.
func (s *Service) WithGH(gh GH) *Service {
	s.gh = gh
	return s
}

// WithRepos sets how owner/repo is mapped to the local checkout that clones are made from.
func (s *Service) WithRepos(resolve RepoResolver) *Service {
	s.resolve = resolve
	return s
}

// WithProjectRepo sets how a project's own GitHub repo (lowercase owner/repo, "" when it has none) is looked up, so the
// runs list can tell the UI which repo a bare PR number means.
func (s *Service) WithProjectRepo(repo func(ctx context.Context, projectID string) string) *Service {
	s.projectRepo = repo
	return s
}

// WithProjectHarness sets how a project's worker harness ("" when it pins none) is looked up, for a start that names no
// harness and whose project has no Litmus harness setting.
func (s *Service) WithProjectHarness(harness func(ctx context.Context, projectID string) string) *Service {
	s.projectHarness = harness
	return s
}

// Reporter posts the worker report `done` for a session, as `ao report --done` would.
type Reporter func(ctx context.Context, sessionID, note string) error

// WithReporter sets how a submitted run reports its session done.
func (s *Service) WithReporter(r Reporter) *Service {
	s.report = r
	return s
}

// Killer stops a tester's AO session. The deadline sweep and a finished run end the tester through the same one.
type Killer func(ctx context.Context, sessionID string) error

// WithKiller sets how a submitted run's tester is stopped, endGrace after its done report.
func (s *Service) WithKiller(k Killer) *Service {
	s.kill = k
	return s
}

// SessionActivity reads a session's activity state ("idle", "exited", ...) and when it last changed. found is false
// when the session is unknown.
type SessionActivity func(ctx context.Context, sessionID string) (state string, since time.Time, found bool)

// WithActivity sets how the sweep detects stopped testers and testers that never ran intake. Without it only the
// run deadline and time since spawn apply.
func (s *Service) WithActivity(a SessionActivity) *Service {
	s.activity = a
	return s
}

func invalid(code, msg string) error  { return apierr.Invalid(code, msg, nil) }
func conflict(code, msg string) error { return apierr.Conflict(code, msg, nil) }

var endGrace = 10 * time.Second

type busyVerb struct {
	since time.Time
	done  chan struct{}
}

func runID(sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", invalid("SESSION_ID_REQUIRED", "sessionId is required")
	}
	return "run_" + sessionID, nil
}

func (s *Service) running(sessionID, verb string) (<-chan struct{}, bool) {
	id, err := runID(sessionID)
	if err != nil {
		return nil, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	fl, on := s.busy[id+" "+verb]
	return fl.done, on
}

func (s *Service) begin(sessionID, verb string) (Run, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(sessionID)
	if err != nil {
		return Run{}, nil, err
	}
	if strings.HasPrefix(verb, "launch ") {
		err = s.appDeadline(run)
	}
	if err != nil {
		return Run{}, nil, err
	}
	done, err := s.markBusy(run.ID, verb)
	return run, done, err
}

func (s *Service) markBusy(runID, verb string) (func(), error) {
	key := runID + " " + verb
	if _, on := s.busy[key]; on {
		return nil, conflict("TESTING_BUSY", verb+" is already in progress for "+runID+"; poll `ao testing status`")
	}
	closing, launching := verb == "submit" || verb == "cleanup", strings.HasPrefix(verb, "launch ")
	for k := range s.busy {
		other, ours := strings.CutPrefix(k, runID+" ")
		if ours && ((closing && strings.HasPrefix(other, "launch ")) || (launching && (other == "submit" || other == "cleanup"))) {
			return nil, conflict("TESTING_RUN_BUSY", fmt.Sprintf("%s is refused while %s is in progress for %s; wait for it to finish", verb, other, runID))
		}
	}
	fl := busyVerb{since: s.clock(), done: make(chan struct{})}
	s.busy[key] = fl
	return func() { s.mu.Lock(); delete(s.busy, key); s.mu.Unlock(); close(fl.done) }, nil
}

func (s *Service) update(id string, fn func(*Run)) (Run, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.store.Get(id)
	if err != nil {
		return Run{}, err
	}
	fn(&run)
	return run, s.store.Save(run)
}

func (s *Service) load(sessionID string) (Run, error) {
	id, err := runID(sessionID)
	if err != nil {
		return Run{}, err
	}
	run, err := s.store.Get(id)
	if errors.Is(err, fs.ErrNotExist) {
		return Run{}, apierr.NotFound("TESTING_RUN_NOT_FOUND", "no testing run for this session; start with `ao testing intake <pr-url>`")
	}
	if err != nil {
		return Run{}, err
	}
	// Prepare has its own bounded context and resets the testing deadline when it finishes.
	// A status poll must not expire it against the earlier intake deadline.
	_, preparing := s.busy[run.ID+" prepare"]
	if !run.State.Closed() && !preparing && !s.clock().Before(run.Deadline.Add(writeupGrace)) {
		run.State = RunExpired
		if err := s.store.Save(run); err != nil {
			return Run{}, err
		}
	}
	if run.State.Closed() {
		why := ""
		if run.State == RunExpired && run.Error != "" {
			why = ": " + run.Error
		}
		return Run{}, conflict("TESTING_RUN_CLOSED", fmt.Sprintf("run %s is %s%s; testing verbs are refused", run.ID, run.State, why))
	}
	return run, nil
}

// App actions end at the deadline; submit and status stay open during the write-up grace.
func (s *Service) appDeadline(run Run) error {
	if !s.clock().Before(run.Deadline) {
		return conflict("TESTING_RUN_CLOSED", "app action deadline reached; submit the verdict within the 5 minute write-up grace")
	}
	return nil
}

type redactedError struct {
	error
	text string
}

func (e redactedError) Error() string { return e.text }
func (e redactedError) Unwrap() error { return e.error }
func scrubError(err error, scrub func(string) string) error {
	if err == nil {
		return nil
	}
	return redactedError{err, scrub(err.Error())}
}

func (s *Service) ghRun(ctx context.Context, args ...string) ([]byte, error) {
	out, err := s.gh.Run(ctx, args...)
	return out, scrubError(err, s.scrub)
}

func (s *Service) command(ctx context.Context, dir string, env []string, name string, args ...string) ([]byte, error) {
	out, err := s.runner.Run(ctx, dir, env, name, args...)
	return out, scrubError(err, s.scrub)
}

func (s *Service) runMedia(ctx context.Context, args ...string) error {
	return scrubError(s.media.Run(ctx, args...), s.scrub)
}

// scrub hides local paths in reports.
func (s *Service) scrub(msg string) string {
	if d := s.store.dataDir(); d != "" {
		msg = strings.NewReplacer(d, "<data dir>", mdText(d), "<data dir>").Replace(msg)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		msg = strings.NewReplacer(home, "~", mdText(home), "~").Replace(msg)
	}
	return msg
}

func (s *Service) Status(ctx context.Context, sessionID string) (RunStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(sessionID)
	if err != nil {
		return RunStatus{}, err
	}
	var flying []InFlight // what is still running for the run, so a poller can tell a long build from a stuck one
	for k, fl := range s.busy {
		if verb, ours := strings.CutPrefix(k, run.ID+" "); ours {
			flying = append(flying, InFlight{Verb: verb, Seconds: s.clock().Sub(fl.since).Round(time.Second).Seconds()})
		}
	}
	slices.SortFunc(flying, func(a, b InFlight) int { return strings.Compare(a.Verb, b.Verb) })
	return RunStatus{OwnerLogins: s.ownerLoginStatus(ctx, run), Run: run, InFlight: flying}, nil
}
