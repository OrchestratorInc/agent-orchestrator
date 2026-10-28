package litmus

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const (
	// IntakeBudget caps fetching and pinning the PR before prepare begins.
	IntakeBudget = 10 * time.Minute
	maxIssues    = 3
	// prFields is what gh is asked for: the PR's words and ids. Its file list is not wanted (gh cuts it at 100) and
	// neither is its rendered diff (GitHub refuses one past 300 files or 20,000 lines); git in the mirror gives both.
	prFields = "number,title,body,state,labels,isDraft,author,baseRefName,baseRefOid,headRefName,headRefOid,changedFiles,additions,deletions,comments,reviews,closingIssuesReferences,statusCheckRollup,url"
	ghBudget = 45 * time.Second // one gh call: the words have to fit the CLI's call, the code does not depend on them
	maxPatch = 5 << 20          // diff.patch is cut here with a trailer; files.txt is never cut
	// gapNoWords opens the gap of a run that never read the PR's text; its verdict is held for approval (see closeRun).
	gapNoWords = "I could not read the PR's text on GitHub"
)

// GH runs the gh CLI and returns its stdout. Intake only reads through it.
type GH interface {
	Run(ctx context.Context, args ...string) ([]byte, error)
}

type execGH struct{}

// Run executes gh with bounded output; a failure carries gh's stderr. A caller's own deadline is the timeout (a video
// upload gets 90 s), else one minute.
func (execGH) Run(ctx context.Context, args ...string) ([]byte, error) {
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Minute)
		defer cancel()
	}
	out, errOut := limitBuf{max: 32 << 20}, limitBuf{max: 64 << 10}
	cmd := process.CommandContext(ctx, "gh", args...)
	cmd.Env = append(os.Environ(), "GH_PROMPT_DISABLED=1", "NO_COLOR=1", "GH_NO_UPDATE_NOTIFIER=1")
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	switch msg := strings.TrimSpace(errOut.String()); {
	case err == nil:
		return out.Bytes(), nil
	case ctx.Err() != nil:
		return out.Bytes(), ctx.Err()
	case out.over:
		return out.Bytes(), fmt.Errorf("gh output exceeds %d MiB", out.max>>20)
	case msg != "":
		return out.Bytes(), errors.New(msg)
	}
	return out.Bytes(), err
}

// limitBuf fails the command instead of growing past max.
type limitBuf struct {
	bytes.Buffer
	max  int
	over bool
}

func (b *limitBuf) Write(p []byte) (int, error) {
	if b.Len()+len(p) > b.max {
		b.over = true
		return 0, errors.New("output limit")
	}
	return b.Buffer.Write(p)
}

// ghFailure turns a gh error into an actionable API error.
func ghFailure(err error, what string) error {
	msg := err.Error()
	low := strings.ToLower(msg)
	has := func(subs ...string) bool {
		return slices.ContainsFunc(subs, func(s string) bool { return strings.Contains(low, s) })
	}
	line, _, _ := strings.Cut(msg, "\n")
	line = string([]rune(line)[:min(len([]rune(line)), 200)])
	switch {
	case has("executable file not found"):
		return apierr.Unavailable("TESTING_GH_MISSING", "the gh CLI is not installed or not on PATH")
	case has("rate limit"):
		return apierr.TooManyRequests("TESTING_GH_RATE_LIMIT", "GitHub rate limit reached; retry later")
	case has("gh auth login", "authentication", "http 401", "bad credentials", "not logged in"):
		return apierr.Forbidden("TESTING_GH_AUTH", "gh is not authenticated; run `gh auth login`")
	case has("could not resolve", "http 404", "not found"):
		return apierr.NotFound("TESTING_GH_NOT_FOUND", what+" not found: "+line)
	}
	return apierr.Unavailable("TESTING_GH_FAILED", "gh failed fetching "+what+": "+line)
}

var shortRef = regexp.MustCompile(`^([A-Za-z0-9_.-]+)/([A-Za-z0-9_.-]+)#(\d+)$`)

// ParsePR accepts https://github.com/O/R/pull/N and O/R#N. The repo is lowercased.
func ParsePR(s string) (repo string, number int, err error) {
	s = strings.TrimSpace(s)
	if m := shortRef.FindStringSubmatch(s); m != nil {
		s = "https://github.com/" + m[1] + "/" + m[2] + "/pull/" + m[3]
	}
	ref, err := domain.ParseChangeRequestURL(s)
	if err != nil || ref.Provider != "github" || ref.Host != "github.com" {
		return "", 0, domain.ErrInvalidChangeRequestURL
	}
	return ref.Repository, ref.Number, nil
}

// remark is a PR comment, a review or an issue comment.
type remark struct {
	Author                 struct{ Login string }
	Body, State            string
	CreatedAt, SubmittedAt time.Time
}

func (r remark) at() time.Time { return cmp.Or(r.SubmittedAt, r.CreatedAt) }

type pullRequest struct {
	Number                                           int
	Title, Body, State, URL                          string
	IsDraft                                          bool
	Author                                           struct{ Login string }
	BaseRefName, BaseRefOid, HeadRefName, HeadRefOid string
	ChangedFiles, Additions, Deletions               int
	Comments, Reviews                                []remark
	ClosingIssuesReferences                          []struct {
		Number     int
		Repository struct {
			Name  string
			Owner struct{ Login string }
		}
	}
	StatusCheckRollup []struct{ Name, Context, Conclusion, Status, State string }
}

// changedFile is one line of files.txt: what the PR did to a path and how much, from git in the mirror.
type changedFile struct {
	Status         string // A, M, D, R, C or T: the first letter of git's name-status
	Path, From     string // From is the old path of a rename or copy
	Added, Deleted int
	Binary         bool // git gives no counts
}

type intakeData struct {
	pr       pullRequest
	document map[string]json.RawMessage // original GitHub fields, paginated discussions and any read failures
	refs     []targetReference
	number   int
	words    bool // gh read the PR's text; false when the ids came from git alone
	files    []changedFile
	diff     string // the patch from the fork point, cut at maxPatch
	// issues are the closed issues gh could read, at most maxIssues, in the order the PR names them.
	issues []IssueRef
	repo   string // as gh reports it, so a renamed repo resolves to its new name
	gaps   []string
}

// Intake pins the PR and saves its raw context. The session's run starts its clock
// here; prepare restarts it when the builds are ready. Two things are fatal: the PR cannot be found, and its two commits cannot be brought into Litmus's mirror of
// the repo. Everything else GitHub fails to give (the PR's text, a linked issue, its file count) becomes a line of
// Run.Gaps in run.json, verdict.md and the comment. A fatal error leaves the run in intake with Error set;
// because the run has no pinned SHAs, intake may be retried in place, and a retry while the first call is
// still at work (a cold mirror clone) waits for it. When no registered AO project holds the repo, the run is pinned
// anyway with Error set to "blocked at prepare: ...", so status says why.
func (s *Service) Intake(ctx context.Context, sessionID, prRef string) (Run, error) {
	kind, repo, number, err := ParseTarget(prRef)
	if err != nil {
		return Run{}, invalid("TESTING_PR_URL_INVALID", "intake needs a GitHub pull request or issue URL")
	}
	id, err := runID(sessionID)
	if err != nil {
		return Run{}, err
	}
	run, done, err := s.claimIntake(ctx, id, sessionID, repo, number, kind)
	if err != nil {
		return Run{}, err
	}
	if done == nil { // the call this one waited for pinned the run
		return run, nil
	}
	defer done()
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), IntakeBudget) // a cold mirror clone outlives the CLI's call; a retry waits for this one
	defer cancel()

	if kind == "issue" {
		return s.intakeIssue(ctx, run)
	}
	d, ferr := s.pullWords(ctx, repo, number)                 // network, so the service lock is not held
	if ferr != nil && !isCode(ferr, "TESTING_GH_NOT_FOUND") { // gh down, slow, rate-limited or logged out: the ids come from git
		d, ferr = s.pullIDs(ctx, repo, number, ferr)
	}
	blocked := ""
	if ferr == nil && s.resolve != nil { // prepare would fail on the same lookup: say so now, not as a bare 404 later
		if err := s.localCheckout(ctx, d.repo, repo); err != nil {
			blocked = "blocked at prepare: " + err.Error()
		}
	}
	if ferr == nil && blocked != "" { // prepare will refuse, so a repo nobody registered is not cloned for a pack
		d.gaps = append(d.gaps, "I did not read the PR's code with git: no AO project holds this repo, so the changed files and the diff are missing.")
	} else if ferr == nil {
		_, ferr = s.pullCode(ctx, &d)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if cur, err := s.store.Get(id); err != nil || cur.State != RunIntake || cur.HeadSHA != "" {
		return Run{}, conflict("TESTING_RUN_EXISTS", "run "+id+" changed while intake was fetching")
	}
	if ferr == nil {
		for name, data := range map[string]string{"files.txt": filesText(d.files), "diff.patch": d.diff} {
			if ferr = s.store.WriteFile(id, name, []byte(data)); ferr != nil {
				break
			}
		}
	}
	if ferr == nil {
		if d.document == nil {
			// Git-only intake still records the known identity and names the missing words.
			d.document = map[string]json.RawMessage{}
			for key, value := range map[string]any{"number": number, "url": run.URL(), "title": nil, "body": nil, "state": nil, "labels": nil, "comments": nil, "reviews": nil, "reviewComments": nil, "unavailable": d.gaps} {
				d.document[key], _ = json.Marshal(value)
			}
		}
		if d.refs == nil {
			d.refs = []targetReference{}
		}
		ci := d.document["statusCheckRollup"]
		if len(ci) == 0 {
			ci = json.RawMessage("null")
		}
		for name, data := range map[string]any{"pr.json": d.document, "refs.json": d.refs, "ci.json": ci} {
			if ferr = s.store.put(id, name, data); ferr != nil {
				break
			}
		}
	}
	if ferr == nil {
		ferr = s.store.writeRecipeContext(id, d.repo)
	}
	pinned := run
	if ferr == nil {
		pinned.Issues, pinned.Gaps = d.issues, d.gaps
		pinned.Title = clip(strings.TrimSpace(d.pr.Title), 200)
		pinned.Repo, pinned.BaseSHA, pinned.HeadSHA, pinned.BaseRef, pinned.Error = d.repo, d.pr.BaseRefOid, d.pr.HeadRefOid, d.pr.BaseRefName, blocked
		ferr = s.store.writeTarget(pinned)
		if ferr == nil {
			ferr = s.store.writeContextPack(pinned)
		}
	}
	if ferr != nil {
		run.Error = ferr.Error()
		if err := s.store.Save(run); err != nil {
			ferr = errors.Join(ferr, err)
		}
		return run, ferr
	}
	return pinned, s.store.Save(pinned)
}

// isCode reports whether err is the API error with that code.
func isCode(err error, code string) bool {
	var e *apierr.Error
	return errors.As(err, &e) && e.Code == code
}

// claimIntake creates or restarts the session's run and marks intake in flight. A second call while one is at work
// (a retry after a CLI timeout) waits for it; when that call pinned the run, the run is returned with a nil done, so
// a slow mirror clone is never done twice.
func (s *Service) claimIntake(ctx context.Context, id, sessionID, repo string, number int, kind string) (Run, func(), error) {
	spawn, markerFound, err := s.store.waitForSpawned(ctx, sessionID, s.spawnMarkerWait)
	if err != nil {
		return Run{}, nil, err
	}
	markerNumber := spawn.PR
	if spawn.Kind == "issue" {
		markerNumber = spawn.Issue
	}
	if markerFound && (spawn.Repo != repo || markerNumber != number || cmp.Or(spawn.Kind, "pr") != kind) {
		markerFound = false
	}
	commentMode := CommentOff // a direct or incomplete start cannot safely opt into posting
	if markerFound {
		commentMode = s.store.commentMode(sessionID)
	}
	evidenceMode := s.store.evidenceMode(sessionID)

	waited := false
	for {
		s.mu.Lock()
		if fl, on := s.busy[id+" intake"]; on {
			s.mu.Unlock()
			select {
			case <-fl.done:
			case <-ctx.Done():
				return Run{}, nil, ctx.Err()
			}
			waited = true
			continue
		}
		if cur, err := s.store.Get(id); waited && err == nil && cur.pinned() {
			s.mu.Unlock()
			return cur, nil, nil
		}
		run, err := s.claim(id, sessionID, repo, number, commentMode, evidenceMode, kind)
		var done func()
		if err == nil {
			done, err = s.markBusy(id, "intake")
		}
		s.mu.Unlock()
		return run, done, err
	}
}

// claim creates the session's run, or restarts a failed intake in place. Any
// other existing run, pinned or closed, is a conflict. Callers hold s.mu.
func (s *Service) claim(id, sessionID, repo string, number int, commentMode, evidenceMode, kind string) (Run, error) {
	prev, err := s.store.Get(id)
	switch {
	case errors.Is(err, fs.ErrNotExist):
	case err != nil:
		return Run{}, err
	case prev.State != RunIntake || prev.pinned():
		return Run{}, conflict("TESTING_RUN_EXISTS", "this session already has run "+id)
	}
	now := s.clock()
	run := Run{Kind: kind, ID: id, SessionID: sessionID, ProjectID: s.store.sessionProject(sessionID), Repo: repo, RequestedRepo: repo, PRNumber: number, State: RunIntake, CreatedAt: now, Comment: commentMode, Evidence: evidenceMode}
	if kind == "issue" {
		run.IssueNumber, run.PRNumber = number, 0
	}
	run.Deadline = now.Add(run.budget())
	if err := s.store.Save(run); err != nil {
		return run, err
	}
	if prev.ID == "" {
		if err := s.store.put(id, ownerLoginsFile, readOwnerLogins(context.Background())); err != nil {
			return run, err
		}
	}

	return run, nil
}

// pullWords reads the PR's words through gh: its text, ids, branches, comments, reviews, CI and linked issues. A PR gh
// cannot find is asked for once more (a new one can be unknown for a few seconds) and then fatal; a linked issue gh
// cannot read is a gap, never fatal. Failed discussion pages preserve the PR and completed pages.
// Failure to read the PR itself is returned for Intake to fall back to git.
func (s *Service) pullWords(ctx context.Context, repo string, number int) (intakeData, error) {
	n := strconv.Itoa(number)
	d := intakeData{repo: repo, number: number}
	raw, err := s.readGH(ctx, "pr", "view", n, "--repo", repo, "--json", prFields)
	if err != nil && isCode(ghFailure(err, "pull request"), "TESTING_GH_NOT_FOUND") {
		select {
		case <-time.After(s.retryPause):
		case <-ctx.Done():
		}
		raw, err = s.readGH(ctx, "pr", "view", n, "--repo", repo, "--json", prFields)
	}
	if err != nil {
		return d, ghFailure(err, "pull request")
	}
	if err := json.Unmarshal(raw, &d.pr); err != nil || d.pr.HeadRefOid == "" || d.pr.BaseRefOid == "" {
		return d, errors.New("gh returned a pull request without base and head commits")
	}
	if err := json.Unmarshal(raw, &d.document); err != nil {
		return d, err
	}
	d.words = true
	if r, _, err := ParsePR(d.pr.URL); err == nil {
		d.repo = r
	}
	keys := bodyIssues(d.pr.Body, d.repo)
	linked := append([]issueKey{}, keys...)
	for _, ref := range d.pr.ClosingIssuesReferences {
		irepo := d.repo
		if o, name := ref.Repository.Owner.Login, ref.Repository.Name; o != "" && name != "" {
			irepo = strings.ToLower(o + "/" + name)
		}
		key := issueKey{irepo, ref.Number}
		if !slices.Contains(linked, key) {
			linked = append(linked, key)
		}
	}
	if len(keys) == 0 {
		keys = linked[:min(len(linked), maxIssues)]
	}
	var incomplete []string
	var comments []json.RawMessage
	for _, field := range []struct{ name, endpoint string }{
		{"comments", "issues/" + n + "/comments"},
		{"reviews", "pulls/" + n + "/reviews"},
		{"reviewComments", "pulls/" + n + "/comments"},
	} {
		items, err := s.readGHPages(ctx, "repos/"+d.repo+"/"+field.endpoint+"?per_page=100", field.name)
		if err != nil {
			failure := err.Error()
			incomplete = append(incomplete, failure)
			d.gaps = append(d.gaps, gapNoWords+" completely: "+failure)
			if len(items) == 0 && len(d.document[field.name]) > 0 {
				if err := json.Unmarshal(d.document[field.name], &items); err != nil {
					return d, err
				}
			}
		}
		d.document[field.name], err = json.Marshal(items)
		if err != nil {
			return d, err
		}
		comments = append(comments, items...)
	}
	if len(incomplete) > 0 {
		d.document["incomplete"], err = json.Marshal(incomplete)
		if err != nil {
			return d, err
		}
	}
	// Save linked issue text and comments in refs.json, without following their references.
	refs, gaps, err := s.targetReferences(ctx, d.repo, number, d.pr.Body, comments, linked)
	if err != nil {
		return d, err
	}
	d.refs, d.gaps = refs, append(d.gaps, gaps...)
	for _, key := range keys {
		for _, ref := range refs {
			if ref.Repo == key.repo && ref.Number == key.number && ref.State != "" {
				d.issues = append(d.issues, IssueRef{Number: ref.Number, Title: strings.TrimSpace(ref.Title), URL: ref.URL})
			}
		}
	}
	return d, nil
}

// ghReason says in a few words why a gh call failed, for a gap.
func ghReason(err error) string {
	switch e := ghFailure(err, ""); {
	case errors.Is(err, context.DeadlineExceeded):
		return "gh timed out"
	case isCode(e, "TESTING_GH_RATE_LIMIT"):
		return "GitHub's rate limit"
	case isCode(e, "TESTING_GH_AUTH"):
		return "gh is not logged in"
	case isCode(e, "TESTING_GH_MISSING"):
		return "gh is not installed"
	case isCode(e, "TESTING_GH_NOT_FOUND"):
		return "not found"
	}
	line, _, _ := strings.Cut(strings.TrimSpace(err.Error()), "\n")
	return clip(line, 120)
}

// pullIDs is intake without GitHub's words: the head id from refs/pull/N/head and the base from the default branch's
// tip, both read by git in the mirror. No such pull ref is "not found"; a GitHub git cannot reach either is fatal, as
// nothing can be tested. why is the gh failure, said in the gap.
func (s *Service) pullIDs(ctx context.Context, repo string, number int, why error) (intakeData, error) {
	d := intakeData{repo: repo, number: number}
	mirror := s.store.mirrorDir(repo)
	mu := s.mirrorLock(mirror)
	mu.Lock()
	defer mu.Unlock()
	if err := s.ensureMirror(ctx, repo); err != nil {
		return d, apierr.Unavailable("TESTING_COMMITS_MISSING", fmt.Sprintf("gh failed (%s) and git could not reach GitHub either: %s", ghReason(why), gitReason(err)))
	}
	pull := fmt.Sprintf("refs/pull/%d/head", number)
	out, err := s.git(ctx, mirror, "-c", ghCredentials, "ls-remote", "--symref", "origin", "HEAD", pull)
	if err != nil {
		return d, apierr.Unavailable("TESTING_COMMITS_MISSING", fmt.Sprintf("gh failed (%s) and git could not reach GitHub either: %s", ghReason(why), gitReason(err)))
	}
	d.pr = pullRequest{Number: number, URL: fmt.Sprintf("https://github.com/%s/pull/%d", repo, number)}
	for _, l := range strings.Split(out, "\n") {
		left, right, _ := strings.Cut(l, "\t")
		switch {
		case right == pull:
			d.pr.HeadRefOid = left
		case right == "HEAD" && strings.HasPrefix(left, "ref: refs/heads/"):
			d.pr.BaseRefName = strings.TrimPrefix(left, "ref: refs/heads/")
		case right == "HEAD":
			d.pr.BaseRefOid = left
		}
	}
	if d.pr.HeadRefOid == "" || d.pr.BaseRefOid == "" {
		return d, apierr.NotFound("TESTING_GH_NOT_FOUND", fmt.Sprintf("pull request #%d not found: gh failed (%s) and GitHub has no %s", number, ghReason(why), pull))
	}
	d.gaps = append(d.gaps, fmt.Sprintf("%s (%s), so I tested without its title, body, comments or linked issue and compared against the tip of %s.", gapNoWords, ghReason(why), cmp.Or(d.pr.BaseRefName, "the default branch")))
	return d, nil
}

// patchArgs pin git's patch format so no setting of the user's (diff.noprefix, diff.mnemonicPrefix, core.quotePath, color,
// an external diff or textconv driver) changes what splitDiff reads: "diff --git a/<path> b/<path>" with the path as it
// is. Git still quotes a path that holds a quote, a backslash, a tab or a newline; splitDiff finds no path for such a file.
var patchArgs = []string{"-c", "diff.noprefix=false", "-c", "diff.mnemonicPrefix=false", "-c", "core.quotePath=false",
	"diff", "--patch", "-M", "--no-color", "--no-ext-diff", "--no-textconv", "--src-prefix=a/", "--dst-prefix=b/"}

// forkGapPrefix starts the gap that says the base build is the base branch where the PR branched, not its tip; the
// comment reads it to say so where it introduces the two builds.
const forkGapPrefix = "The PR branched from "

// pullCode brings the PR's two commits into the mirror, pins them, and reads the changes with git: the file list and
// counts since the fork point (base...head) with no cap, and the patch, stopped at maxPatch. A head that moved since gh
// answered is taken from refs/pull/N/head with a gap. A base whose tip is past the fork point is replaced by the fork
// point (pinned too), with a gap that says how many commits back it is, unless the fork point is the head (the PR is already in the base): then the base keeps its tip. A commit id that is not one, or both commits failing to arrive
// after a retry, is fatal. Returns the mirror containing the pinned commits.
func (s *Service) pullCode(ctx context.Context, d *intakeData) (string, error) {
	for _, id := range []string{d.pr.HeadRefOid, d.pr.BaseRefOid} { // from gh or ls-remote: never into git's argv, a ref name or a path unchecked
		if !shaPattern.MatchString(id) {
			return "", apierr.Unavailable("TESTING_COMMITS_MISSING", fmt.Sprintf("the PR's commits could not be fetched: %q is not a commit id", clip(id, 40)))
		}
	}
	mirror := s.store.mirrorDir(d.repo)
	mu := s.mirrorLock(mirror)
	mu.Lock()
	defer mu.Unlock()
	if err := s.ensureMirror(ctx, d.repo); err != nil {
		return "", apierr.Unavailable("TESTING_COMMITS_MISSING", "could not make Litmus's mirror of "+d.repo+": "+gitReason(err))
	}
	head, base := d.pr.HeadRefOid, d.pr.BaseRefOid
	err := s.fetchInto(ctx, mirror, head, base)
	if err != nil { // the author may have pushed since gh answered: the pull ref's tip is the PR now
		if perr := s.fetchInto(ctx, mirror, fmt.Sprintf("refs/pull/%d/head", d.number)); perr == nil {
			if moved, rerr := s.git(ctx, mirror, "rev-parse", "FETCH_HEAD"); rerr == nil && moved != head {
				if err = s.fetchInto(ctx, mirror, base); err == nil {
					d.gaps = append(d.gaps, fmt.Sprintf("The PR's commit changed while I was reading it; I tested %.8s.", moved))
					head, d.pr.HeadRefOid = moved, moved
				}
			}
		}
	}
	if err != nil {
		return "", apierr.Unavailable("TESTING_COMMITS_MISSING", fmt.Sprintf("the PR's commits %.8s and %.8s could not be fetched into the mirror: %s", head, base, gitReason(err)))
	}
	if err := s.pin(ctx, mirror, head, base); err != nil {
		return "", apierr.Unavailable("TESTING_COMMITS_MISSING", "could not pin the PR's commits in the mirror: "+gitReason(err))
	}
	fork, err := s.git(ctx, mirror, "merge-base", base, head)
	if err != nil || fork == "" {
		fork = base
		d.gaps = append(d.gaps, "I found no fork point between the PR and its base, so the changes I read are against the base commit and may include unrelated ones.")
	} else if fork != base && fork != head && shaPattern.MatchString(fork) { // the base build is main where the PR branched, so a difference is the PR's, not other people's commits; a fork point that is the head (a merged PR) would make both builds the same checkout, so the base keeps its tip
		if out, cerr := s.git(ctx, mirror, "rev-list", "--count", fork+".."+base); cerr == nil {
			if behind, aerr := strconv.Atoi(out); aerr == nil {
				if err := s.pin(ctx, mirror, fork); err != nil {
					return "", apierr.Unavailable("TESTING_COMMITS_MISSING", "could not pin the PR's branch point in the mirror: "+gitReason(err))
				}
				ref, commits := cmp.Or(d.pr.BaseRefName, "main"), "commits"
				if behind == 1 {
					commits = "commit"
				}
				d.pr.BaseRefOid = fork
				d.gaps = append(d.gaps, fmt.Sprintf("%s%s %d %s ago; '%s' in this report is that branch point (%.8s), not today's %s.", forkGapPrefix, ref, behind, commits, ref, fork, ref))
			}
		}
	}
	files, filesErr := s.changedFiles(ctx, mirror, fork, head)
	d.files = files
	if filesErr != nil {
		d.gaps = append(d.gaps, "I could not read the PR's file list with git ("+gitReason(filesErr)+"), so the areas it changes are unknown.")
	}
	patch, cut, patchErr := s.gitCapped(ctx, mirror, maxPatch, append(slices.Clone(patchArgs), fork, head)...)
	switch {
	case patchErr != nil:
		d.gaps = append(d.gaps, "I could not read the PR's changes with git ("+gitReason(patchErr)+"); diff.patch is empty.")
	case cut:
		trailer := fmt.Sprintf("\n[diff.patch cut at %s; `git diff %s...%s -- <path>` in the PR build checkout shows the rest]\n", humanBytes(maxPatch), base, head)
		patch = patch[:min(len(patch), maxPatch-len(trailer))]
		if i := strings.LastIndexByte(patch, '\n'); i > 0 {
			patch = patch[:i]
		}
		d.gaps = append(d.gaps, fmt.Sprintf("The PR's patch is longer than %s; I was shown the first %s of it and read the rest with git (files.txt lists every file).", humanBytes(maxPatch), humanBytes(maxPatch)))
		d.diff = patch + trailer
	case patch != "":
		d.diff = patch
		if !strings.HasSuffix(patch, "\n") && len(patch) < maxPatch {
			d.diff += "\n"
		}
	}
	if d.words && filesErr == nil && d.pr.ChangedFiles != len(d.files) {
		d.gaps = append(d.gaps, fmt.Sprintf("GitHub counts %d changed files and git found %d from the fork point; the PR may have moved while I was reading it.", d.pr.ChangedFiles, len(d.files)))
	}
	return mirror, nil
}

// changedFiles is the PR's file list from the fork point: git's name-status for what happened to each path and its
// numstat for how much, both with renames detected, as files.txt and the pack show them.
func (s *Service) changedFiles(ctx context.Context, mirror, fork, head string) ([]changedFile, error) {
	counts, err := s.gitRaw(ctx, mirror, "diff", "--numstat", "-z", "-M", fork, head)
	if err != nil {
		return nil, err
	}
	names, err := s.gitRaw(ctx, mirror, "diff", "--name-status", "-z", "-M", fork, head)
	if err != nil {
		return nil, err
	}
	byPath := map[string]changedFile{}
	for toks := strings.Split(counts, "\x00"); len(toks) > 0; toks = toks[1:] { // "added\tdeleted\tpath", or "added\tdeleted\t" then old, new
		a, rest, ok := strings.Cut(toks[0], "\t")
		del, path, ok2 := strings.Cut(rest, "\t")
		if !ok || !ok2 {
			continue
		}
		if path == "" && len(toks) > 2 {
			path, toks = toks[2], toks[2:]
		}
		f := changedFile{Path: path, Binary: a == "-"}
		f.Added, _ = strconv.Atoi(a)
		f.Deleted, _ = strconv.Atoi(del)
		byPath[path] = f
	}
	var files []changedFile
	for toks := strings.Split(names, "\x00"); len(toks) > 1; { // "M", path; or "R100", old, new
		status, path, from := toks[0], toks[1], ""
		toks = toks[2:]
		if (strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")) && len(toks) > 0 {
			from, path, toks = path, toks[0], toks[1:]
		}
		if status == "" || path == "" {
			continue
		}
		f := byPath[path]
		f.Status, f.Path, f.From = status[:1], path, from
		files = append(files, f)
	}
	return files, nil
}

// filesText is files.txt: one line per changed file, tab-separated, after a header that says the columns.
func filesText(files []changedFile) string {
	var b strings.Builder
	b.WriteString("# status\tadded\tdeleted\tpath\t[from]  (git diff -M base...head in the mirror; - means binary)\n")
	for _, f := range files {
		a, d := strconv.Itoa(f.Added), strconv.Itoa(f.Deleted)
		if f.Binary {
			a, d = "-", "-"
		}
		fmt.Fprintf(&b, "%s\t%s\t%s\t%s", f.Status, a, d, f.Path)
		if f.From != "" {
			fmt.Fprintf(&b, "\t%s", f.From)
		}
		b.WriteString("\n")
	}
	return b.String()
}

// paths are the changed files' paths in order.
func paths(files []changedFile) []string {
	out := make([]string, len(files))
	for i, f := range files {
		out[i] = f.Path
	}
	return out
}

type issueKey struct {
	repo   string // lowercase owner/repo
	number int
}

// name is how the pack and a gap call the issue: #N in the PR's own repo, owner/repo#N elsewhere.
func (k issueKey) name(repo string) string {
	if k.repo == repo {
		return "#" + strconv.Itoa(k.number)
	}
	return k.repo + "#" + strconv.Itoa(k.number)
}

var (
	htmlComment  = regexp.MustCompile(`(?s)<!--.*?-->`)
	issueURL     = regexp.MustCompile(`https://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)`)
	closingIssue = regexp.MustCompile(`(?i)\b(?:close[sd]?|fix(?:e[sd])?|resolve[sd]?):?\s+(?:https://github\.com/([\w.-]+/[\w.-]+)/issues/(\d+)|([\w.-]+/[\w.-]+)?#(\d+))`)
)

// bodyIssues are the issues a PR body says it closes ("Closes #N", "Fixes owner/repo#N", "Resolves <issue URL>"), or
// its first issue URL when it has no such phrase. At most maxIssues, in order, without repeats; repo is where a bare #N lives.
func bodyIssues(body, repo string) (out []issueKey) {
	body = htmlComment.ReplaceAllString(body, "") // a template's commented-out "Closes #123" is not a claim
	add := func(r, n string) bool {
		num, err := strconv.Atoi(n)
		k := issueKey{strings.ToLower(cmp.Or(r, repo)), num}
		if err != nil || num <= 0 || slices.Contains(out, k) {
			return false
		}
		out = append(out, k)
		return len(out) == maxIssues
	}
	for _, m := range closingIssue.FindAllStringSubmatch(body, -1) {
		if add(cmp.Or(m[1], m[3]), cmp.Or(m[2], m[4])) {
			break
		}
	}
	if m := issueURL.FindStringSubmatch(body); len(out) == 0 && m != nil {
		add(m[1], m[2])
	}
	return out
}
