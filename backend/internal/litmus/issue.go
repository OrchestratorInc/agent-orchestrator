package litmus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

var issueTargetURL = regexp.MustCompile(`^https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/issues/([1-9][0-9]*)/?$`)

// ParseIssue accepts an explicit GitHub issue URL. Short references remain PR references.
func ParseIssue(s string) (repo string, number int, err error) {
	m := issueTargetURL.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return "", 0, errors.New("expected https://github.com/O/R/issues/N")
	}
	return ParsePR("https://github.com/" + m[1] + "/pull/" + m[2])
}

// ParseTarget distinguishes explicit issue URLs from existing PR references without a network lookup.
func ParseTarget(s string) (kind, repo string, number int, err error) {
	if repo, number, err = ParseIssue(s); err == nil {
		return "issue", repo, number, nil
	}
	repo, number, err = ParsePR(s)
	if err != nil {
		return "", "", 0, err
	}
	return "pr", repo, number, nil
}

func (s *Service) readGH(ctx context.Context, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, ghBudget)
	defer cancel()
	return s.ghRun(ctx, args...)
}

// issueTarget reads the default branch and its exact tip. Start records this identity before spawning,
// so the issue prompt and the worker's intake cannot select different tips.
func (s *Service) issueTarget(ctx context.Context, repo string) (branch, sha string, err error) {
	raw, err := s.readGH(ctx, "api", "repos/"+repo)
	if err != nil {
		return "", "", ghFailure(err, "repository")
	}
	var meta struct {
		DefaultBranch string `json:"default_branch"`
	}
	if err := json.Unmarshal(raw, &meta); err != nil {
		return "", "", fmt.Errorf("read repository metadata: %w", err)
	}
	if meta.DefaultBranch == "" {
		return "", "", apierr.Unavailable("TESTING_COMMITS_MISSING", "GitHub returned no default branch")
	}
	branch = meta.DefaultBranch
	ref := "refs/heads/" + branch
	if _, err := s.git(ctx, s.store.dataDir(), "check-ref-format", ref); err != nil {
		return "", "", fmt.Errorf("invalid default branch: %w", err)
	}
	out, err := s.git(ctx, s.store.dataDir(), "-c", ghCredentials, "ls-remote", "https://github.com/"+repo+".git", ref)
	if err != nil {
		return "", "", apierr.Unavailable("TESTING_COMMITS_MISSING", "read default branch tip: "+gitReason(err))
	}
	for _, line := range strings.Split(out, "\n") {
		id, name, ok := strings.Cut(line, "\t")
		if ok && name == ref {
			if sha != "" || !fullSHA.MatchString(id) {
				return "", "", apierr.Unavailable("TESTING_COMMITS_MISSING", "git returned an invalid default branch tip")
			}
			sha = id
		}
	}
	if sha == "" {
		return "", "", apierr.Unavailable("TESTING_COMMITS_MISSING", "git returned no default branch tip")
	}
	return branch, sha, nil
}

var fullSHA = regexp.MustCompile(`^[0-9a-f]{40}$`)

type issueDocument struct {
	Number   int               `json:"number"`
	URL      string            `json:"url"`
	Title    string            `json:"title"`
	Body     string            `json:"body"`
	State    string            `json:"state"`
	Labels   []json.RawMessage `json:"labels"`
	Comments []json.RawMessage `json:"comments"`
}

func (s *Service) issueWords(ctx context.Context, repo string, number int) (issueDocument, error) {
	endpoint := "repos/" + repo + "/issues/" + strconv.Itoa(number)
	raw, err := s.readGH(ctx, "api", endpoint)
	if err != nil {
		return issueDocument{}, ghFailure(err, "issue")
	}
	var in struct {
		Number             int
		Title, Body, State string
		Labels             []json.RawMessage
		PullRequest        json.RawMessage `json:"pull_request"`
	}
	if err := json.Unmarshal(raw, &in); err != nil {
		return issueDocument{}, fmt.Errorf("read issue: %w", err)
	}
	if in.Number != number || in.State == "" || len(in.PullRequest) > 0 && string(in.PullRequest) != "null" {
		return issueDocument{}, invalid("TESTING_ISSUE_INVALID", "GitHub did not return the requested issue")
	}
	comments, err := s.readGHPages(ctx, endpoint+"/comments?per_page=100", "issue comment")
	if err != nil {
		return issueDocument{}, ghFailure(err, "issue comment")
	}
	return issueDocument{Number: number, URL: (Run{Kind: "issue", Repo: repo, IssueNumber: number}).URL(), Title: in.Title, Body: in.Body, State: in.State, Labels: in.Labels, Comments: comments}, nil
}

// readGHPages preserves completed page arrays, even if a later gh API page fails.
func (s *Service) readGHPages(ctx context.Context, endpoint, what string) ([]json.RawMessage, error) {
	raw, callErr := s.readGH(ctx, "api", "--paginate", endpoint)
	items := []json.RawMessage{}
	dec := json.NewDecoder(bytes.NewReader(raw))
	pages := 0
	for {
		var page []json.RawMessage
		err := dec.Decode(&page)
		if err == io.EOF {
			break
		}
		if err != nil {
			return items, fmt.Errorf("%s page %d: %w", what, pages+1, errors.Join(err, callErr))
		}
		if page == nil {
			return items, fmt.Errorf("%s page %d: %w", what, pages+1, errors.Join(errors.New("GitHub returned a null page"), callErr))
		}
		pages++
		items = append(items, page...)
	}
	if callErr != nil {
		return items, fmt.Errorf("%s page %d: %w", what, pages+1, callErr)
	}
	if pages == 0 {
		return items, fmt.Errorf("%s page 1: GitHub returned no page", what)
	}
	return items, nil
}

type targetReference struct {
	Kind     string            `json:"kind"`
	Repo     string            `json:"repo"`
	Number   int               `json:"number"`
	URL      string            `json:"url"`
	Match    string            `json:"match"` // literal body/comment text; empty means GitHub linked it but the text never names it
	Title    string            `json:"title,omitempty"`
	State    string            `json:"state,omitempty"`
	Body     string            `json:"body,omitempty"`
	Comments []json.RawMessage `json:"comments,omitempty"`
	Error    string            `json:"error,omitempty"`
}

var targetReferencePattern = regexp.MustCompile(`https://github\.com/([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+)/(issues|pull)/([1-9][0-9]*)|(?:([A-Za-z0-9_.-]+/[A-Za-z0-9_.-]+))?#([1-9][0-9]*)`)

// issueReferences reads only direct references in the issue's body and comments. Fetched bodies are never scanned.
func (s *Service) issueReferences(ctx context.Context, repo string, doc issueDocument) ([]targetReference, []string, error) {
	return s.targetReferences(ctx, repo, doc.Number, doc.Body, doc.Comments, nil)
}

func (s *Service) targetReferences(ctx context.Context, repo string, number int, body string, comments []json.RawMessage, linked []issueKey) ([]targetReference, []string, error) {
	texts := make([]string, 0, len(linked)+1+len(comments))
	linkedIssues := make(map[issueKey]bool, len(linked))
	for _, key := range linked {
		linkedIssues[key] = true
		texts = append(texts, fmt.Sprintf("https://github.com/%s/issues/%d", key.repo, key.number))
	}
	texts = append(texts, body)
	for _, raw := range comments {
		var c struct{ Body string }
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, nil, fmt.Errorf("read reference comment: %w", err)
		}
		texts = append(texts, c.Body)
	}
	refs := []targetReference{}
	seen := map[issueKey]int{{repo, number}: -1}
	var gaps []string
	for i, text := range texts {
		for _, m := range targetReferencePattern.FindAllStringSubmatch(htmlComment.ReplaceAllString(text, ""), -1) {
			r, n := repo, m[5]
			if m[1] != "" {
				r, n = m[1], m[3]
			} else if m[4] != "" {
				r = m[4]
			}
			number, err := strconv.Atoi(n)
			if err != nil {
				return nil, nil, fmt.Errorf("read reference number: %w", err)
			}
			key := issueKey{strings.ToLower(r), number}
			match := ""
			if i >= len(linked) {
				match = m[0]
			}
			if index, ok := seen[key]; ok {
				if index >= 0 && refs[index].Match == "" {
					refs[index].Match = match
				}
				continue
			}
			if len(refs) == 20 {
				continue
			}
			seen[key] = len(refs)
			ref := targetReference{Kind: "issue", Repo: key.repo, Number: number, Match: match}
			raw, err := s.readGH(ctx, "api", fmt.Sprintf("repos/%s/issues/%d", key.repo, number))
			if err == nil {
				var in struct {
					Title, State, Body string
					PullRequest        json.RawMessage `json:"pull_request"`
				}
				err = json.Unmarshal(raw, &in)
				if err == nil && in.State == "" {
					err = errors.New("GitHub returned a reference without state")
				}
				if err == nil {
					ref.Title, ref.State, ref.Body = in.Title, in.State, in.Body
					if len(in.PullRequest) > 0 && string(in.PullRequest) != "null" {
						ref.Kind = "pr"
					}
				}
			}
			path := "issues"
			if ref.Kind == "pr" || m[2] == "pull" {
				path, ref.Kind = "pull", "pr"
			}
			ref.URL = fmt.Sprintf("https://github.com/%s/%s/%d", key.repo, path, number)
			if err == nil && linkedIssues[key] {
				ref.Comments, err = s.readGHPages(ctx, fmt.Sprintf("repos/%s/issues/%d/comments?per_page=100", key.repo, number), "linked issue comments")
			}
			if err != nil {
				ref.Error = err.Error()
				gaps = append(gaps, "Could not read reference "+ref.URL+": "+ref.Error)
			}
			refs = append(refs, ref)
		}
	}
	return refs, gaps, nil
}

func (s *Service) intakeIssue(ctx context.Context, run Run) (Run, error) {
	pinned, err := s.fetchIssue(ctx, run)
	if err == nil {
		return pinned, nil
	}
	_, saveErr := s.update(run.ID, func(cur *Run) {
		if !cur.State.Closed() {
			cur.Error = err.Error()
		}
	})
	return run, errors.Join(err, saveErr)
}

func (s *Service) fetchIssue(ctx context.Context, run Run) (Run, error) {
	sp, marked := s.store.spawnedSession(run.SessionID)
	if marked && sp.Kind == "issue" && sp.Repo == run.Repo && sp.Issue == run.IssueNumber {
		run.TargetSHA, run.BaseRef, run.CandidateURL = sp.TargetSHA, sp.TargetBranch, sp.CandidateURL
	}
	if run.TargetSHA == "" {
		branch, sha, err := s.issueTarget(ctx, run.Repo)
		if err != nil {
			return run, err
		}
		run.BaseRef, run.TargetSHA = branch, sha
	}
	if !fullSHA.MatchString(run.TargetSHA) {
		return run, invalid("TESTING_COMMITS_MISSING", "issue target is not a full commit SHA")
	}
	doc, err := s.issueWords(ctx, run.Repo, run.IssueNumber)
	if err != nil {
		return run, err
	}
	refs, gaps, err := s.issueReferences(ctx, run.Repo, doc)
	if err != nil {
		return run, err
	}
	run.Title, run.Gaps = doc.Title, gaps
	if run.CandidateURL != "" {
		repo, number, err := ParsePR(run.CandidateURL)
		if err != nil || repo != run.Repo {
			return run, invalid("TESTING_CANDIDATE_INVALID", "candidate must be a pull request in the issue's repository")
		}
		raw, err := s.readGH(ctx, "pr", "view", strconv.Itoa(number), "--repo", repo, "--json", "headRefOid")
		if err != nil {
			return run, ghFailure(err, "candidate pull request")
		}
		var candidate struct{ HeadRefOid string }
		if err := json.Unmarshal(raw, &candidate); err != nil {
			return run, err
		}
		if !fullSHA.MatchString(candidate.HeadRefOid) {
			return run, invalid("TESTING_COMMITS_MISSING", "candidate has no full head commit SHA")
		}
		run.HeadSHA = candidate.HeadRefOid
	}
	if s.resolve != nil {
		if err := s.localCheckout(ctx, run.Repo, run.RequestedRepo); err != nil {
			return run, err
		}
	}
	mirror := s.store.mirrorDir(run.Repo)
	mu := s.mirrorLock(mirror)
	mu.Lock()
	err = s.ensureMirror(ctx, run.Repo)
	var commits []string
	for _, l := range run.targets() {
		commits = append(commits, l.SHA)
	}
	if err == nil {
		err = s.fetchInto(ctx, mirror, commits...)
	}
	if err == nil {
		err = s.pin(ctx, mirror, commits...)
	}
	mu.Unlock()
	if err != nil {
		return run, fmt.Errorf("pin issue commits: %w", err)
	}
	files, err := s.gitRaw(ctx, mirror, "ls-tree", "-r", "--name-only", "-z", run.TargetSHA)
	if err != nil {
		return run, fmt.Errorf("read pinned issue files: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	cur, err := s.store.Get(run.ID)
	if err != nil {
		return run, err
	}
	if cur.State != RunIntake || cur.pinned() {
		return run, conflict("TESTING_RUN_EXISTS", "run "+run.ID+" changed while intake was fetching")
	}
	if err := s.store.put(run.ID, "issue.json", doc); err != nil {
		return run, err
	}
	if err := s.store.put(run.ID, "refs.json", refs); err != nil {
		return run, err
	}
	if err := s.store.WriteFile(run.ID, "files.txt", []byte(strings.ReplaceAll(files, "\x00", "\n"))); err != nil {
		return run, err
	}
	if err := s.store.writeRecipeContext(run.ID, run.Repo); err != nil {
		return run, err
	}
	if err := s.store.writeTarget(run); err != nil {
		return run, err
	}
	if err := s.store.writeContextPack(run); err != nil {
		return run, err
	}
	run.Error = ""
	return run, s.store.Save(run)
}
