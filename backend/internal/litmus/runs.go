package litmus

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// verdictFile is the submitted Verdict, kept for a run that waits for approval so its post can be composed later.
const verdictFile = "verdict.json"

// CommentAwaiting is the comment state of a run whose comment is written and not posted; CommentDiscarded is the one
// of a run whose comment was never posted because the user threw it away.
const (
	CommentAwaiting  = "awaiting-approval"
	CommentDiscarded = "discarded"
)

// Verdict names a run row can carry besides the overall outcomes: the run expired, or has no verdict.
const (
	verdictExpired = "expired"
	verdictNone    = "none"
)

// DefaultRunsLimit and MaxRunsLimit bound a runs listing.
const (
	DefaultRunsLimit = 50
	MaxRunsLimit     = 200
)

// ProjectSettings are the Litmus preferences of one AO project.
type ProjectSettings struct {
	// AskBeforePosting makes a run stop at awaiting-approval with its comment written, until the comment is posted or
	// discarded.
	AskBeforePosting bool `json:"askBeforePosting" description:"When on, a finished run writes its comment and waits for approval instead of posting it."`
	// Review adds a REQUEST_CHANGES review to the comment of a failing verdict. Off by default: the comment alone.
	Review bool `json:"review" description:"When on, a failing verdict also submits a REQUEST_CHANGES review beside its comment. Off by default."`
	// Harness is the agent the project's testers run on. Empty falls back to the project's worker harness when testers
	// can run on it, then to claude-code.
	Harness string `json:"harness,omitempty" description:"Agent the project's testers run on: claude-code or codex. Empty uses the project's worker agent, then claude-code." enum:"claude-code,codex"`
	// Model and Effort are what the project's testers run on. Empty falls back to the project's worker settings, then to
	// TesterModel (claude-code only) and TesterEffort.
	Model  string `json:"model,omitempty" description:"Model the project's testers run on. Empty uses the project's worker model, then the Litmus default."`
	Effort string `json:"effort,omitempty" description:"Reasoning effort the project's testers run at. Empty uses the project's worker effort, then the Litmus default."`
}

// RunPR names the pull request a run tests.
type RunPR struct {
	Number int    `json:"number"`
	URL    string `json:"url"`
	Title  string `json:"title,omitempty"` // empty before intake finished
}

// RunSummary is one row of the runs list.
type RunSummary struct {
	ID        string   `json:"id"`
	SessionID string   `json:"sessionId"`
	ProjectID string   `json:"projectId,omitempty"` // empty on runs started before projects were recorded
	Repo      string   `json:"repo"`
	PR        RunPR    `json:"pr"`
	State     RunState `json:"state"`
	// Verdict is the overall outcome read from verdict.md: pass, fail, partial or blocked; expired for a run that ran
	// out of time, none for a run with no verdict yet.
	Verdict   string    `json:"verdict" enum:"pass,fail,partial,blocked,expired,none"`
	StartedAt time.Time `json:"startedAt"`
	// DurationSeconds runs from intake to the verdict (or the last change of a run that never got one); a run still in
	// flight counts up to now. Time spent waiting for approval is not in it.
	DurationSeconds int64  `json:"durationSeconds"`
	CommentURL      string `json:"commentUrl,omitempty"`
	CommentState    string `json:"commentState,omitempty"` // posted | updated | off | awaiting-approval | discarded | failed:<reason>
	VideoState      string `json:"videoState,omitempty"`
	Error           string `json:"error,omitempty"`
}

// RunList is the answer of the runs list.
type RunList struct {
	Runs []RunSummary `json:"runs"`
	// Repo is the project's own GitHub repo (lowercase owner/repo), which a bare PR number is read against; empty when
	// the project has none on GitHub or the listing is not for one project.
	Repo string `json:"repo,omitempty"`
}

// RunDetail is a run row with the texts and checks a detail view renders.
type RunDetail struct {
	RunSummary
	Run       Run     `json:"run"`
	VerdictMD string  `json:"verdictMd"` // verdict.md; empty before a verdict
	CommentMD string  `json:"commentMd"` // comment.md as written for the PR (before its images are linked, while awaiting approval)
	Checks    []Check `json:"checks"`
}

// Rerun is the tester a rerun spawned. RunID is the id its run will have once the tester ran intake.
type Rerun struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
	RunID     string `json:"runId"`
}

func projectKey(id string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return "", apierr.Invalid("TESTING_PROJECT_INVALID", fmt.Sprintf("invalid project id %q", id), nil)
	}
	return id, nil
}

func (s *Store) projectPath(id string) (string, error) {
	id, err := projectKey(id)
	if err != nil {
		return "", err
	}
	return filepath.Join(filepath.Dir(s.root), "projects", id+".json"), nil
}

// projectSettings reads a project's preferences; a project never configured has the defaults (everything off).
func (s *Store) projectSettings(id string) (ps ProjectSettings, err error) {
	path, err := s.projectPath(id)
	if err != nil {
		return ps, err
	}
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return ps, nil
	}
	if err != nil {
		return ps, err
	}
	err = json.Unmarshal(data, &ps)
	return ps, err
}

func (s *Store) setProjectSettings(id string, ps ProjectSettings) error {
	path, err := s.projectPath(id)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	data, err := json.MarshalIndent(ps, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(path, data)
}

// setSessionProject remembers which project a tester session was started in, for the run it will make:
// <dataDir>/litmus/session-project/<sessionID>.
func (s *Store) setSessionProject(sessionID, projectID string) error {
	if projectID = strings.TrimSpace(projectID); projectID == "" {
		return nil
	}
	if sessionID == "" || filepath.Base(sessionID) != sessionID {
		return fmt.Errorf("invalid session id %q", sessionID)
	}
	dir := filepath.Join(filepath.Dir(s.root), "session-project")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, sessionID), []byte(projectID))
}

func (s *Store) sessionProject(sessionID string) string {
	if sessionID == "" || filepath.Base(sessionID) != sessionID {
		return ""
	}
	data, err := os.ReadFile(filepath.Join(filepath.Dir(s.root), "session-project", sessionID))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// LoadProjectSettings reads a project's Litmus preferences from the data dir without a Service, for the session
// manager that spawns the tester.
func LoadProjectSettings(dataDir, projectID string) (ProjectSettings, error) {
	return NewStore(dataDir).projectSettings(projectID)
}

// ProjectSettings returns the project's Litmus preferences.
func (s *Service) ProjectSettings(_ context.Context, projectID string) (ProjectSettings, error) {
	return s.store.projectSettings(projectID)
}

// SetProjectSettings replaces the project's Litmus preferences. A run that closes afterwards obeys them.
func (s *Service) SetProjectSettings(_ context.Context, projectID string, ps ProjectSettings) (ProjectSettings, error) {
	if ps.Harness = strings.TrimSpace(ps.Harness); ps.Harness != "" && !Supported(ps.Harness) {
		return ps, invalid("TESTING_HARNESS_UNSUPPORTED", fmt.Sprintf("testing workers run on %s or %s, not %q", Harness, HarnessCodex, ps.Harness))
	}
	return ps, s.store.setProjectSettings(projectID, ps)
}

// mustAsk reports whether the run's comment waits for approval: there is a comment to post (the run was not started
// with --no-comment and the verdict is not blocked) and either the run never read the PR's words, or its project asks
// before posting.
func (s *Service) mustAsk(run Run, v Verdict) bool {
	if run.Comment != CommentPost || v.Overall == CheckBlocked {
		return false
	}
	if run.noWords() { // a verdict made without knowing what the PR claims never lands on the PR by itself
		return true
	}
	if run.ProjectID == "" {
		return false
	}
	ps, err := s.store.projectSettings(run.ProjectID)
	return err == nil && ps.AskBeforePosting
}

// reviewOn reports whether the run's project asked for a REQUEST_CHANGES review on a failing verdict.
func (s *Service) reviewOn(run Run) bool {
	if run.ProjectID == "" {
		return false
	}
	ps, err := s.store.projectSettings(run.ProjectID)
	return err == nil && ps.Review
}

var verdictHead = regexp.MustCompile(`^# Verdict: (pass|fail|partial|blocked)\b`)

// summarize builds the list row of a run from its folder.
func (s *Service) summarize(run Run) RunSummary {
	dir := filepath.Join(s.store.root, run.ID)
	row := RunSummary{
		ID: run.ID, SessionID: run.SessionID, ProjectID: run.ProjectID, Repo: run.Repo,
		PR:    RunPR{Number: run.PRNumber, URL: fmt.Sprintf("https://github.com/%s/pull/%d", run.Repo, run.PRNumber), Title: s.title(run, dir)},
		State: run.State, Verdict: verdictNone, StartedAt: run.CreatedAt,
		CommentURL: run.CommentURL, CommentState: run.CommentState, VideoState: run.VideoState, Error: run.Error,
	}
	end := s.clock()
	if run.State.Closed() {
		end = run.CreatedAt // the fallback below keeps a never-judged run's duration at zero
		if st, err := os.Stat(filepath.Join(dir, "verdict.md")); err == nil {
			end = st.ModTime()
		} else if st, err := os.Stat(filepath.Join(dir, "run.json")); err == nil {
			end = st.ModTime()
		}
	}
	row.DurationSeconds = max(0, int64(end.Sub(run.CreatedAt)/time.Second))
	if head := readHead(filepath.Join(dir, "verdict.md"), 200); head != "" {
		if m := verdictHead.FindStringSubmatch(head); m != nil {
			row.Verdict = m[1]
		}
	}
	if row.Verdict == verdictNone && run.State == RunExpired {
		row.Verdict = verdictExpired
	}
	return row
}

// title is the PR title a row shows: the one intake stored on the run. A run from before intake stored it takes it from
// its context pack once and, when it is closed, keeps it in run.json so later polls do not read the pack again.
func (s *Service) title(run Run, dir string) string {
	if run.Title != "" {
		return run.Title
	}
	title := packTitle(dir)
	if title != "" && run.State.Closed() {
		_, _ = s.update(run.ID, func(cur *Run) { cur.Title = cmp.Or(cur.Title, title) })
	}
	return title
}

// readHead returns the first n bytes of a file as text, or "".
func readHead(path string, n int64) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer func() { _ = f.Close() }()
	data, _ := io.ReadAll(io.LimitReader(f, n))
	return string(data)
}

// packTitle is the PR title the run's context pack states, or "".
func packTitle(runDir string) string {
	pack, _ := os.ReadFile(filepath.Join(runDir, "context-pack.md"))
	if m := prTitle.FindSubmatch(pack); len(m) > 1 {
		return clip(strings.TrimSpace(string(m[1])), 200)
	}
	return ""
}

// projectOf is the project a run belongs to. A run recorded without one (older than project tracking) takes it from the
// marker of the session that made it, once, and keeps it in run.json; with no marker it belongs to no project.
func (s *Service) projectOf(run Run) Run {
	if run.ProjectID != "" {
		return run
	}
	project := s.store.sessionProject(run.SessionID)
	if project == "" {
		return run
	}
	run.ProjectID = project
	if cur, err := s.update(run.ID, func(cur *Run) { cur.ProjectID = cmp.Or(cur.ProjectID, project) }); err == nil {
		run = cur
	}
	return run
}

// Runs lists the runs of a project, newest first; a run that cannot be tied to the project is not listed. An empty
// projectID lists every run.
func (s *Service) Runs(ctx context.Context, projectID string, limit int) (RunList, error) {
	if limit <= 0 {
		limit = DefaultRunsLimit
	}
	limit = min(limit, MaxRunsLimit)
	projectID = strings.TrimSpace(projectID)
	runs, err := s.store.List()
	if err != nil {
		return RunList{}, err
	}
	out := RunList{Runs: []RunSummary{}}
	if projectID != "" && s.projectRepo != nil {
		out.Repo = s.projectRepo(ctx, projectID)
	}
	for _, run := range slices.Backward(runs) {
		if projectID != "" {
			if run = s.projectOf(run); run.ProjectID != projectID {
				continue
			}
		}
		out.Runs = append(out.Runs, s.summarize(run))
		if len(out.Runs) == limit {
			break
		}
	}
	return out, nil
}

// RunDetail returns one run with its verdict.md, comment.md and checks.
func (s *Service) RunDetail(_ context.Context, id string) (RunDetail, error) {
	run, err := s.run(id)
	if err != nil {
		return RunDetail{}, err
	}
	checks, err := s.store.Checks(run.ID)
	if err != nil {
		return RunDetail{}, err
	}
	text := func(name string) string {
		data, _ := os.ReadFile(filepath.Join(s.store.root, run.ID, name))
		return string(data)
	}
	return RunDetail{RunSummary: s.summarize(run), Run: run, VerdictMD: text("verdict.md"), CommentMD: text("comment.md"), Checks: append([]Check{}, checks...)}, nil
}

// run loads a run by id, answering not-found when it does not exist.
func (s *Service) run(id string) (Run, error) {
	run, err := s.store.Get(id)
	if errors.Is(err, fs.ErrNotExist) {
		return Run{}, apierr.NotFound("TESTING_RUN_NOT_FOUND", "no testing run "+id)
	}
	return run, err
}

// Rerun starts a new tester on the PR of an earlier run, in the same project and with the same comment and evidence
// modes. projectID is used for a run that did not record its project.
func (s *Service) Rerun(ctx context.Context, id, projectID string) (Rerun, error) {
	run, err := s.run(id)
	if err != nil {
		return Rerun{}, err
	}
	project := cmp.Or(run.ProjectID, strings.TrimSpace(projectID))
	if project == "" {
		return Rerun{}, invalid("TESTING_PROJECT_REQUIRED", "this run did not record its project; pass projectId")
	}
	started, err := s.Start(ctx, StartRequest{
		ProjectID: project, PRURL: fmt.Sprintf("https://github.com/%s/pull/%d", run.Repo, run.PRNumber),
		NoComment: run.Comment != CommentPost, NoEvidence: run.Evidence != EvidencePost,
	})
	if err != nil {
		return Rerun{}, err
	}
	return Rerun{SessionID: started.SessionID, Name: started.Name, RunID: RunID(started.SessionID)}, nil
}

// awaiting loads a run that waits for approval and marks its post or discard in flight, so two decisions cannot race.
func (s *Service) awaiting(id string) (Run, func(), error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, err := s.run(id)
	if err != nil {
		return Run{}, nil, err
	}
	if run.State != RunAwaitingApproval {
		return Run{}, nil, conflict("TESTING_NOT_AWAITING", fmt.Sprintf("run %s is %s; only a run awaiting approval can be posted or discarded", run.ID, run.State))
	}
	if run.CommentState == CommentDiscarded {
		return Run{}, nil, conflict("TESTING_DISCARDED", fmt.Sprintf("run %s was discarded; its comment will not be posted", run.ID))
	}
	done, err := s.markBusy(run.ID, "post") // post and discard share the key
	return run, done, err
}

// Post publishes the evidence and posts the comment of a run awaiting approval: the second half of what submit does for
// a project that does not ask. When the post fails the run stays awaiting approval, with the reason in its comment
// state, so it can be tried again.
func (s *Service) Post(ctx context.Context, id string) (Run, error) {
	run, done, err := s.awaiting(id)
	if err != nil {
		return Run{}, err
	}
	defer done()
	if run.Comment != CommentPost {
		return Run{}, conflict("TESTING_COMMENT_OFF", "this run was started with --no-comment; it cannot be posted")
	}
	var v Verdict
	if err := s.store.read(run.ID, verdictFile, &v); err != nil {
		return Run{}, fmt.Errorf("read the verdict of %s: %w", run.ID, err)
	}
	runDir := filepath.Join(s.store.root, run.ID)
	body, err := os.ReadFile(filepath.Join(runDir, "comment.md"))
	if err != nil {
		return Run{}, fmt.Errorf("read comment.md of %s: %w", run.ID, err)
	}
	// The publish runs on its own context: a client that hangs up between the evidence push and the comment would leave
	// the post half done. Every step has its own timeout, as in submit.
	url, state, ev := s.publishComment(context.WithoutCancel(ctx), run, v, string(body))
	return s.update(run.ID, func(cur *Run) {
		cur.CommentURL, cur.CommentState, cur.EvidenceState, cur.EvidenceCommit = url, state, ev.state, ev.commit
		cur.VideoState, cur.VideoURLs = ev.videoState, ev.videoURLs()
		if !strings.HasPrefix(state, "failed:") {
			cur.State = RunSubmitted
		}
	})
}

const (
	oneVideoPostBudget = uploadTimeout + 2*time.Minute + 15*time.Second
	// PostBudget bounds login, image staging, the evidence push and repo lookup, two videos with one retry each, and the PR comment.
	PostBudget = commentTimeout + stageTimeout + pushTimeout + time.Minute + 2*oneVideoPostBudget + commentTimeout
	// SubmitBudget allows two frame encodes, then captioned cuts, before publication.
	SubmitBudget = PostBudget + 2*stopTimeout + 3*videoTimeout + 2*time.Duration(screencastFrameCap)*100*time.Millisecond
)

// Discard closes a run awaiting approval without posting: its targets are cleaned up and its comment is kept in the
// run folder, unposted.
func (s *Service) Discard(ctx context.Context, id string) (Run, error) { return s.discard(ctx, id, "") }

// discard is Discard; why, when set, is written to the run's comment.log. When the teardown fails the run stays as it
// was, awaiting approval with a comment that can still be posted or discarded again.
func (s *Service) discard(ctx context.Context, id, why string) (Run, error) {
	run, done, err := s.awaiting(id)
	if err != nil {
		return Run{}, err
	}
	defer done()
	if failed := s.teardown(ctx, run); failed != nil { // submit already tore the targets down; this makes sure
		return run, failed
	}
	out, err := s.update(run.ID, func(cur *Run) {
		cur.State, cur.CommentState = RunCleaned, CommentDiscarded
		for i := range cur.Legs {
			cur.Legs[i].State = "cleaned"
		}
	})
	if err == nil && why != "" {
		s.commentLogf(run, "%s", why)
	}
	return out, err
}
