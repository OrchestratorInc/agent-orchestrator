package litmus

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var outcomes = []CheckState{CheckPass, CheckFail, CheckPartial, CheckAbsent, CheckSkipped}

// endGrace is how long a tester has after its run closes to print its last line before its session is stopped.
// Tests shorten it.
var endGrace = 10 * time.Second

// Service owns the run lifecycle and the check ledger. Every verb is keyed by
// the calling AO session, which owns at most one run.
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
		s.binary, _ = resolveBinary(os.Getenv("AO_AGENT_BROWSER_PATH"), exe)
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

// runID names the one run a session may own, so a second intake cannot reset
// the run's clock and every verb finds its run without a scan.
func runID(sessionID string) (string, error) {
	if strings.TrimSpace(sessionID) == "" {
		return "", invalid("SESSION_ID_REQUIRED", "sessionId is required")
	}
	return "run_" + sessionID, nil
}

// Status returns the run and its checks.
func (s *Service) Status(ctx context.Context, sessionID string) (RunStatus, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(sessionID)
	if err != nil {
		return RunStatus{}, err
	}
	checks, err := s.store.Checks(run.ID)
	var flying []InFlight // what is still running for the run, so a poller can tell a long build from a stuck one
	for k, fl := range s.busy {
		if verb, ours := strings.CutPrefix(k, run.ID+" "); ours {
			flying = append(flying, InFlight{Verb: verb, Seconds: s.clock().Sub(fl.since).Round(time.Second).Seconds()})
		}
	}
	slices.SortFunc(flying, func(a, b InFlight) int { return strings.Compare(a.Verb, b.Verb) })
	return RunStatus{OwnerLogins: s.ownerLoginStatus(ctx, run), Run: run, Checks: append([]Check{}, checks...), InFlight: flying}, err
}

// minChecks must exist before the PR build is launched; there is no upper bound, the run's budget is the only limit.
const minChecks = 2

// CheckAdd appends a pending check with the observable the worker expects. leg is
// "base", "head" or empty (the check acts on whichever leg the worker drives).
func (s *Service) CheckAdd(_ context.Context, sessionID, name, expect, leg string) (Check, error) {
	if leg != "" && leg != "base" && leg != "head" {
		return Check{}, invalid("TESTING_LEG_INVALID", "leg must be base or head")
	}
	name, expect = strings.TrimSpace(name), strings.TrimSpace(expect)
	if name == "" || expect == "" {
		return Check{}, invalid("TESTING_CHECK_INVALID", "a check needs a name and an expected observable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(sessionID)
	if err != nil {
		return Check{}, err
	}
	checks, err := s.store.Checks(run.ID)
	if err != nil {
		return Check{}, err
	}
	for _, c := range checks {
		if c.onLeg() == (Check{Leg: leg}).onLeg() && strings.EqualFold(c.Name, name) {
			return Check{}, conflict("TESTING_CHECK_DUPLICATE", "check "+strconv.Quote(name)+" already exists on the "+c.onLeg()+" leg")
		}
	}
	check := Check{ID: "c" + strconv.Itoa(len(checks)+1), Name: name, Expect: expect, State: CheckPending, Leg: leg}
	return check, s.store.SaveChecks(run.ID, append(checks, check))
}

// CheckStart opens a check so receipts attach to it. One check runs at a time.
func (s *Service) CheckStart(_ context.Context, sessionID, id string) (Check, error) {
	return s.updateCheck(sessionID, id, func(c *Check, all []Check) error {
		for _, other := range all {
			if other.State == CheckRunning {
				return conflict("TESTING_CHECK_STATE", "check "+other.ID+" is still running; end it first")
			}
		}
		if c.State != CheckPending {
			return conflict("TESTING_CHECK_STATE", fmt.Sprintf("check %s is %s; only a pending check can start", c.ID, c.State))
		}
		c.State = CheckRunning
		return nil
	})
}

// CheckEnd closes a running check with its outcome (pass, fail, partial, absent or skipped).
func (s *Service) CheckEnd(ctx context.Context, sessionID, id string, result CheckState, note string, sameFault bool, shot int) (Check, error) {
	if !slices.Contains(outcomes, result) {
		return Check{}, invalid("TESTING_CHECK_RESULT_INVALID", "result must be pass, fail, partial, absent or skipped")
	}
	finish := func(c *Check, all []Check) error {
		if shot != 0 {
			if _, ok := headAfterShot(all, shot); !ok {
				return invalid("TESTING_ARGS_INVALID", "--shot must name a head receipt with a saved after-shot in this run")
			}
		}
		if sameFault && (c.onLeg() != "base" || (result != CheckFail && result != CheckPartial)) {
			return invalid("TESTING_CHECK_SAME_FAULT", "--same-fault requires a base result of fail or partial")
		}
		if c.State != CheckRunning {
			return conflict("TESTING_CHECK_STATE", fmt.Sprintf("check %s is %s; only a running check can end", c.ID, c.State))
		}
		if result != CheckSkipped && !slices.ContainsFunc(c.Receipts, func(r Receipt) bool { return r.onLeg() == c.onLeg() }) {
			return conflict("TESTING_CHECK_NO_RECEIPT", fmt.Sprintf("check %s has no receipt on the %s leg; act on it (click, fill, press, ...) with --leg %s first", c.ID, c.onLeg(), c.onLeg()))
		}
		state, stored, err := checkEvidence(*c, result, strings.TrimSpace(note))
		if err != nil {
			return err
		}
		if sameFault && state != CheckFail && state != CheckPartial {
			return invalid("TESTING_CHECK_SAME_FAULT", "--same-fault requires a base result of fail or partial")
		}
		c.State, c.Note, c.SameFault = state, stored, sameFault
		c.Shot = shot
		return nil
	}
	// Validate before capture, then revalidate when persisting. Never hold the
	// service mutex while waiting for the browser or taking the engine mutex.
	s.mu.Lock()
	run, err := s.load(sessionID)
	var checks []Check
	if err == nil {
		checks, err = s.store.Checks(run.ID)
	}
	s.mu.Unlock()
	if err != nil {
		return Check{}, err
	}
	i := slices.IndexFunc(checks, func(c Check) bool { return c.ID == id })
	if i < 0 {
		return Check{}, apierr.NotFound("TESTING_CHECK_NOT_FOUND", "no check "+strconv.Quote(id))
	}
	c := checks[i]
	if err := finish(&c, checks); err != nil {
		return Check{}, err
	}
	var shots []ThemeShot
	var themeNote string
	if leg, err := pickLeg(run, c.onLeg()); err == nil && c.State != CheckSkipped {
		e := s.engineFor(run.ID, leg)
		e.mu.Lock()
		defer e.mu.Unlock()
		// A competing end may have finished while this call waited for input.
		// Reject it before it can overwrite the accepted check's pictures.
		s.mu.Lock()
		current, readErr := s.store.Checks(run.ID)
		s.mu.Unlock()
		if readErr != nil {
			return Check{}, readErr
		}
		j := slices.IndexFunc(current, func(x Check) bool { return x.ID == id })
		if j < 0 {
			return Check{}, apierr.NotFound("TESTING_CHECK_NOT_FOUND", "no check "+strconv.Quote(id))
		}
		c = current[j]
		if err := finish(&c, current); err != nil {
			return Check{}, err
		}
		shots, themeNote = s.themePair(ctx, e, c.ID)
	}
	return s.updateCheck(sessionID, id, func(c *Check, all []Check) error {
		if err := finish(c, all); err != nil {
			return err
		}
		if len(shots) > 0 || themeNote != "" {
			i := slices.IndexFunc(c.Receipts, func(r Receipt) bool { return r.onLeg() == c.onLeg() })
			// The last action is the screen that this check finished on.
			for j := range c.Receipts {
				if c.Receipts[j].onLeg() == c.onLeg() {
					i = j
				}
			}
			if i < 0 {
				c.Receipts = append(c.Receipts, Receipt{Leg: c.onLeg(), Action: "check end", At: s.clock()})
				i = len(c.Receipts) - 1
			}
			c.Receipts[i].ThemeShots, c.Receipts[i].ThemeNote = shots, themeNote
		}
		return nil
	})
}

// checkEvidence requires a saved screenshot for an outcome unless the check was skipped.
func checkEvidence(c Check, result CheckState, note string) (CheckState, string, error) {
	if result == CheckSkipped || slices.ContainsFunc(c.legReceipts(), func(r Receipt) bool { return fileExists(r.AfterPath) }) {
		return result, note, nil
	}
	return "", "", conflict("TESTING_CHECK_EVIDENCE", "no picture was saved; act again or end it skipped")
}

// busyVerb is a long verb running for a run: when it began, and a channel that
// closes when it ends so a retry can wait for it instead of failing.
type busyVerb struct {
	since time.Time
	done  chan struct{}
}

// running returns the channel that closes when verb finishes for the session's
// run, and false when nothing is in flight.
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

// begin loads the session's open run and marks verb in flight, so a second call
// (a retry after a CLI timeout) gets a conflict instead of racing the first.
func (s *Service) begin(sessionID, verb string) (Run, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(sessionID)
	if err != nil {
		return Run{}, nil, err
	}
	done, err := s.markBusy(run.ID, verb)
	return run, done, err
}

// markBusy marks verb of run in flight and returns the func that clears it. The mutating verbs exclude each other
// across keys: submit and cleanup refuse while any launch runs (they would stop a recording the launch has not
// stored yet), and a launch refuses while a submit or cleanup runs. Callers hold s.mu.
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

// update re-reads the run, applies fn and saves it: long verbs run unlocked and merge their result here.
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

// load returns the session's run, flipping it to expired once the deadline has
// passed, and refuses a submitted, expired or cleaned run: every verb shares
// that refusal. Callers hold s.mu.
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
	if !run.State.Closed() && s.clock().After(run.Deadline) {
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

// updateCheck applies fn to one check and persists the ledger.
func (s *Service) updateCheck(sessionID, id string, fn func(c *Check, all []Check) error) (Check, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.load(sessionID)
	if err != nil {
		return Check{}, err
	}
	checks, err := s.store.Checks(run.ID)
	if err != nil {
		return Check{}, err
	}
	for i := range checks {
		if checks[i].ID != id {
			continue
		}
		if err := fn(&checks[i], checks); err != nil {
			return Check{}, err
		}
		return checks[i], s.store.SaveChecks(run.ID, checks)
	}
	return Check{}, apierr.NotFound("TESTING_CHECK_NOT_FOUND", "no check "+strconv.Quote(id))
}
