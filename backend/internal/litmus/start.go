package litmus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Harness is the agent a testing worker runs on when nothing else is chosen. Claude Code enforces the tool lists
// and dontAsk mode of ToolPolicy; HarnessCodex has no such lists, so only GuardEnv and the skill's rules bind it.
const Harness = "claude-code"

// HarnessCodex is the other agent a testing worker may run on.
const HarnessCodex = "codex"

// Supported reports whether a testing worker may run on harness.
func Supported(harness string) bool { return harness == Harness || harness == HarnessCodex }

// SpawnRequest is what the daemon needs to launch one testing worker.
type SpawnRequest struct {
	ProjectID string
	Harness   string
	Name      string // sidebar label
	Prompt    string // first message
}

// Spawner launches the testing worker's AO session (kind worker, mode tui) and returns its id.
type Spawner func(ctx context.Context, req SpawnRequest) (sessionID string, err error)

// ShellRule is the sentence the spawn prompt and SKILL.md share: what the tester's open toolset is for, and its limits.
const ShellRule = "You have a normal shell and the usual tools: run any command, read code and logs. Put scratch files in `scratch/`; never edit the files Litmus writes in the run folder. Build, open and drive the app windows only through `ao testing`. The base and head source trees Litmus prepared and built are read-only; repos you clone under `scratch/` are test data, so branch, dirty, commit and add `.ao/` files in them whenever the feature needs that state. Never push or post to GitHub."

// ExploreRule is the sentence that tells the tester it is driving a real app and may reach for settings.
const ExploreRule = "Explore the app the way a user would: menus, settings, modes. If a feature hides behind a setting, turn the setting on and say so in your note."

// StartRequest names the PR to test. An empty Harness means the project's Litmus setting, then its worker harness when
// that is supported, then claude-code.
type StartRequest struct {
	ProjectID    string
	Harness      string
	PRURL        string // PR or explicit issue URL
	CandidateURL string
	NoComment    bool // the run's verdict is not posted to the PR
	NoEvidence   bool // no screenshots or GIFs are published for the comment
}

// Started is the session a run will belong to; the worker makes the run itself with `ao testing intake`.
type Started struct {
	SessionID string `json:"sessionId"`
	Name      string `json:"name"`
}

// WithSpawner sets how Start launches workers.
func (s *Service) WithSpawner(sp Spawner) *Service {
	s.spawn = sp
	return s
}

// defaultHarness is the harness of a start that names none: the project's Litmus setting, else its worker harness when
// testers can run on it, else claude-code.
func (s *Service) defaultHarness(ctx context.Context, projectID string) (string, error) {
	if projectID == "" {
		return Harness, nil
	}
	ps, err := s.store.projectSettings(projectID)
	if err != nil {
		return "", fmt.Errorf("litmus settings of project %s: %w", projectID, err)
	}
	if ps.Harness != "" {
		return ps.Harness, nil
	}
	if s.projectHarness != nil {
		if worker := s.projectHarness(ctx, projectID); Supported(worker) {
			return worker, nil
		}
	}
	return Harness, nil
}

// Start spawns a testing worker for a PR. It holds no run: the worker's intake does.
func (s *Service) Start(ctx context.Context, req StartRequest) (Started, error) {
	kind, repo, number, err := ParseTarget(req.PRURL)
	if err != nil {
		return Started{}, invalid("TESTING_PR_URL_INVALID", "start needs a GitHub pull request or issue URL")
	}
	if req.CandidateURL != "" {
		candidateRepo, n, err := ParsePR(req.CandidateURL)
		if kind != "issue" || err != nil || candidateRepo != repo {
			return Started{}, invalid("TESTING_CANDIDATE_INVALID", "candidate requires an issue and a pull request in the same repository")
		}
		req.CandidateURL = fmt.Sprintf("https://github.com/%s/pull/%d", candidateRepo, n)
	}
	harness := strings.TrimSpace(req.Harness)
	if harness == "" {
		if harness, err = s.defaultHarness(ctx, strings.TrimSpace(req.ProjectID)); err != nil {
			return Started{}, err
		}
	}
	if !Supported(harness) {
		return Started{}, invalid("TESTING_HARNESS_UNSUPPORTED", fmt.Sprintf("testing workers run on %s or %s, not %q", Harness, HarnessCodex, harness))
	}
	if s.spawn == nil {
		return Started{}, conflict("TESTING_SPAWN_UNAVAILABLE", "this daemon cannot spawn testing workers")
	}
	if s.resolve != nil { // refuse here, not three tool calls into the worker's run
		if err := s.localCheckout(ctx, repo); err != nil {
			return Started{}, err
		}
	}
	url := fmt.Sprintf("https://github.com/%s/pull/%d", repo, number)
	name := fmt.Sprintf("litmus: PR #%d", number)
	prompt := fmt.Sprintf("Test PR %s. Start with `ao testing intake %s`. %s %s", url, url, ShellRule, ExploreRule)
	marker := spawned{Repo: repo, PR: number, Kind: kind}
	if kind == "issue" {
		branch, sha, err := s.issueTarget(ctx, repo)
		if err != nil {
			return Started{}, err
		}
		url = (Run{Kind: kind, Repo: repo, IssueNumber: number}).URL()
		name = fmt.Sprintf("litmus: issue #%d", number)
		marker.PR, marker.Issue, marker.TargetSHA, marker.TargetBranch, marker.CandidateURL = 0, number, sha, branch, req.CandidateURL
		prompt = fmt.Sprintf("Reproduce issue %s on pinned default-branch SHA %s using leg main. The testing budget is %s after prepare finishes. Read SKILL.md and issue-repro.md first. Start with `ao testing intake %s`. %s %s", url, sha, IssueBudget, url, ShellRule, ExploreRule)
		if req.CandidateURL != "" {
			prompt += " Candidate fix: " + req.CandidateURL + " on optional leg head."
		}
	}
	id, err := s.spawn(ctx, SpawnRequest{
		ProjectID: strings.TrimSpace(req.ProjectID),
		Harness:   harness,
		Name:      name,
		Prompt:    prompt,
	})
	if err != nil {
		return Started{}, err
	}
	for kind, off := range map[string]bool{"comment": req.NoComment, "evidence": req.NoEvidence} { // the worker's intake picks the modes up by session
		if off {
			if err := s.store.markOff(kind, id); err != nil {
				return Started{}, fmt.Errorf("session %s started but its no-%s mode was not saved, so the run may not honor it: %w", id, kind, err)
			}
		}
	}
	if err := s.store.setSessionProject(id, req.ProjectID); err != nil {
		return Started{}, fmt.Errorf("session %s started but its project was not saved, so its run will not be listed under the project: %w", id, err)
	}
	marker.At = s.clock().UTC()
	if err := s.store.markSpawned(id, marker); err != nil {
		return Started{}, fmt.Errorf("session %s started but its spawn time was not saved, so it will not be reaped if it never runs intake: %w", id, err)
	}
	return Started{SessionID: id, Name: name}, nil
}

// spawnedSessions lists the testers started and not yet settled, by session id.
func (s *Store) spawnedSessions() (map[string]spawned, error) {
	entries, err := os.ReadDir(s.spawnedDir())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := map[string]spawned{}
	for _, e := range entries {
		var sp spawned
		if data, err := os.ReadFile(filepath.Join(s.spawnedDir(), e.Name())); err == nil && json.Unmarshal(data, &sp) == nil {
			out[e.Name()] = sp
		}
	}
	return out, nil
}

// spawnedSession reads one tester's spawn marker.
func (s *Store) spawnedSession(sessionID string) (sp spawned, found bool) {
	if sessionID == "" || filepath.Base(sessionID) != sessionID {
		return sp, false
	}
	data, err := os.ReadFile(filepath.Join(s.spawnedDir(), sessionID))
	return sp, err == nil && json.Unmarshal(data, &sp) == nil
}

// WithSpawnMarkerWait sets how long intake waits for Start's marker to appear.
func (s *Service) WithSpawnMarkerWait(wait time.Duration) *Service {
	s.spawnMarkerWait = wait
	return s
}

func (s *Store) clearSpawned(sessionID string) {
	_ = os.Remove(filepath.Join(s.spawnedDir(), sessionID))
}
