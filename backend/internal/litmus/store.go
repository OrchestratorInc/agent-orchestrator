package litmus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/skillassets"
)

// Store keeps run data as plain files.
type Store struct{ root string }

const (
	CommentOff   = "off"
	CommentPost  = "post"
	EvidenceOff  = "off"
	EvidencePost = "post"
)

type spawned struct {
	At           time.Time `json:"at"`
	Repo         string    `json:"repo"`
	PR           int       `json:"pr"`
	Kind         string    `json:"kind,omitempty"`
	Issue        int       `json:"issue,omitempty"`
	TargetSHA    string    `json:"targetSha,omitempty"`
	TargetBranch string    `json:"targetBranch,omitempty"`
	CandidateURL string    `json:"candidateUrl,omitempty"`
}

func NewStore(dataDir string) *Store {
	return &Store{root: filepath.Join(dataDir, "litmus", "runs")}
}

func (s *Store) dataDir() string { return filepath.Dir(filepath.Dir(s.root)) }

// reposDir holds the clones of one repo, one dir per SHA: <dataDir>/litmus/repos/<owner>__<name>.
func (s *Store) reposDir(repo string) string {
	return filepath.Join(filepath.Dir(s.root), "repos", strings.ReplaceAll(repo, "/", "__"))
}

func (s *Store) path(id string, elem ...string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return "", apierr.Invalid("TESTING_ID_INVALID", fmt.Sprintf("invalid run id %q", id), nil)
	}
	return filepath.Join(append([]string{s.root, id}, elem...)...), nil
}

func (s *Store) Save(run Run) error {
	dir, err := s.path(run.ID, "receipts")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	if err := s.put(run.ID, "run.json", run); err != nil {
		return err
	}
	if !run.pinned() {
		return nil
	}
	return s.writeTarget(run)
}

func (s *Store) writeTarget(run Run) error {
	return s.put(run.ID, "target.json", struct {
		Kind         string      `json:"kind"`
		URL          string      `json:"url"`
		TargetSHA    string      `json:"targetSha,omitempty"`
		CandidateURL string      `json:"candidateUrl,omitempty"`
		Legs         []targetLeg `json:"legs"`
	}{run.Kind, run.URL(), run.TargetSHA, run.CandidateURL, run.targets()})
}

func (s *Store) writeRecipeContext(id, repo string) error {
	text, err := s.recipeText(repo)
	if isCode(err, "TESTING_RECIPE_MISSING") {
		// Intake can pin an unconfigured repo; prepare reports the missing recipe.
		return nil
	}
	if err != nil {
		return err
	}
	recipe, err := ParseRecipe([]byte(text))
	if err != nil {
		return apierr.Invalid("TESTING_RECIPE_INVALID", err.Error(), nil)
	}
	if err := s.WriteFile(id, "recipe.yaml", []byte(text)); err != nil {
		return err
	}
	return s.WriteFile(id, "recipe-notes.md", []byte(recipe.Notes))
}

// writeContextPack indexes raw files without interpreting the target's claims.
func (s *Store) writeContextPack(run Run) error {
	legs := []string{}
	for _, leg := range run.targets() {
		legs = append(legs, leg.Name+" "+leg.SHA)
	}
	kind := run.Kind
	if kind == "" {
		kind = "pr"
	}
	skill := filepath.ToSlash(skillassets.LitmusDir(s.dataDir()))
	cli, err := os.Executable()
	if err != nil {
		return fmt.Errorf("resolve tester CLI: %w", err)
	}
	lines := []string{
		"# Litmus context", "", "Read the skill first.",
		"Skill: " + skill + "/SKILL.md",
		"Mode guides: " + skill + "/pr-testing.md; " + skill + "/issue-repro.md",
		"CLI binary: " + cli + "; its directory is first on the tester PATH",
		"URL: " + run.URL(), "Kind: " + kind,
		"Pinned SHAs and legs: " + strings.Join(legs, ", "),
		"Testing budget: " + run.budget().String() + " after prepare finishes",
		"Deadline: " + run.Deadline.UTC().Format(time.RFC3339) + "; prepare resets it; ao testing status shows the current value",
		"", "Files in this run directory:",
		"- context-pack.md: this index",
	}
	for _, file := range []struct{ name, description string }{
		{"run.json", "state, gaps and deadline"},
		{"target.json", "pinned commits, legs and launched ports"},
		{"pr.json", "title, body, labels, state, comments and reviews"},
		{"issue.json", "title, body, labels, state and all comments"},
		{"refs.json", "direct references, title, state, body, linked-issue comments and read failures"},
		{"diff.patch", "PR diff, capped at 5 MiB"},
		{"files.txt", "PR changed files or the pinned issue tree"},
		{"ci.json", "GitHub status checks for the PR"},
		{"recipe.yaml", "resolved recipe"},
		{"recipe-notes.md", "app-specific guidance from the recipe"},
	} {
		path, err := s.path(run.ID, file.name)
		if err != nil {
			return err
		}
		if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
			if file.name == "recipe.yaml" {
				lines = append(lines, "Recipe: unavailable; prepare reports the missing setup")
			}
			continue
		} else if err != nil {
			return err
		}
		if file.name == "pr.json" {
			var doc struct{ Incomplete []string }
			if err := s.read(run.ID, file.name, &doc); err != nil {
				return err
			}
			if len(doc.Incomplete) > 0 {
				file.description += "; incomplete: " + strings.Join(strings.Fields(strings.Join(doc.Incomplete, "; ")), " ")
			}
		}
		lines = append(lines, "- "+file.name+": "+file.description)
	}
	lines = append(lines, "", "Available verbs: ao testing intake, prepare, launch, browser, screenshot, record, status, submit, cleanup")
	return s.WriteFile(run.ID, "context-pack.md", []byte(strings.Join(lines, "\n")+"\n"))
}

func (s *Store) Get(id string) (run Run, err error) {
	err = s.read(id, "run.json", &run)
	return run, err
}

func (s *Store) WriteFile(id, name string, data []byte) error {
	path, err := s.path(id, name)
	if err != nil || filepath.Base(name) != name {
		return fmt.Errorf("invalid run file %q/%q", id, name)
	}
	return writeAtomic(path, data)
}

func (s *Store) put(id, name string, v any) error {
	var data bytes.Buffer
	enc := json.NewEncoder(&data)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return err
	}
	return s.WriteFile(id, name, bytes.TrimSuffix(data.Bytes(), []byte("\n")))
}

func (s *Store) read(id, name string, out any) error {
	path, err := s.path(id, name)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, out)
}

func writeAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-*")
	if err != nil {
		return err
	}
	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Sync()
	}
	if cerr := tmp.Close(); err == nil {
		err = cerr
	}
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err != nil {
		_ = os.Remove(tmp.Name())
	}
	return err
}

func (s *Store) offPath(kind, sessionID string) (string, error) {
	if sessionID == "" || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID {
		return "", fmt.Errorf("invalid session id %q", sessionID)
	}
	return filepath.Join(filepath.Dir(s.root), kind+"-off", sessionID), nil
}

func (s *Store) markOff(kind, sessionID string) error {
	path, err := s.offPath(kind, sessionID)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return writeAtomic(path, []byte("off\n"))
}

func (s *Store) commentMode(sessionID string) string {
	if path, err := s.offPath("comment", sessionID); err == nil && fileExists(path) {
		return CommentOff
	}
	return CommentPost
}

func (s *Store) evidenceMode(sessionID string) string {
	if path, err := s.offPath("evidence", sessionID); err == nil && fileExists(path) {
		return EvidenceOff
	}
	return EvidencePost
}

func (s *Store) spawnedDir() string { return filepath.Join(filepath.Dir(s.root), "spawned") }

func (s *Store) markSpawned(sessionID string, sp spawned) error {
	if sessionID == "" || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID {
		return fmt.Errorf("invalid session id %q", sessionID)
	}
	if err := os.MkdirAll(s.spawnedDir(), 0o750); err != nil {
		return err
	}
	data, err := json.Marshal(sp)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.spawnedDir(), sessionID), data)
}

func (s *Store) waitForSpawned(ctx context.Context, sessionID string, maxWait time.Duration) (spawned, bool, error) {
	if sessionID == "" || sessionID == "." || sessionID == ".." || filepath.Base(sessionID) != sessionID {
		return spawned{}, false, fmt.Errorf("invalid session id %q", sessionID)
	}
	path := filepath.Join(s.spawnedDir(), sessionID)
	read := func() (spawned, bool, error) {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			return spawned{}, false, nil
		}
		if err != nil {
			return spawned{}, false, fmt.Errorf("read spawned marker for session %s: %w", sessionID, err)
		}
		var sp spawned
		if json.Unmarshal(data, &sp) == nil && !sp.At.IsZero() && strings.TrimSpace(sp.Repo) != "" && sp.PR > 0 {
			return sp, true, nil
		}
		return spawned{}, false, nil
	}

	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if err := ctx.Err(); err != nil {
			return spawned{}, false, err
		}
		if sp, found, err := read(); found || err != nil {
			return sp, found, err
		}
		select {
		case <-ctx.Done():
			return spawned{}, false, ctx.Err()
		case <-timer.C:
			if err := ctx.Err(); err != nil {
				return spawned{}, false, err
			}
			return read()
		case <-ticker.C:
		}
	}
}

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

func (s *Store) List() ([]Run, error) {
	entries, err := os.ReadDir(s.root)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var runs []Run
	for _, e := range entries {
		if run, err := s.Get(e.Name()); err == nil {
			runs = append(runs, run)
		}
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.Before(runs[j].CreatedAt) })
	return runs, nil
}

func (s *Store) legDir(runID, leg string) string { return filepath.Join(s.root, runID, leg) }

func (s *Store) Receipts(id string) ([]Receipt, error) {
	path, err := s.path(id, "actions.jsonl")
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var receipts []Receipt
	dec := json.NewDecoder(f)
	for {
		var a actionRec
		if err := dec.Decode(&a); err != nil {
			if errors.Is(err, io.EOF) {
				return receipts, nil
			}
			return nil, fmt.Errorf("read actions.jsonl: %w", err)
		}
		receipts = append(receipts, Receipt{Seq: a.Seq, Leg: a.Leg, Action: a.Action, Theme: a.Theme, Args: a.Args, BeforePath: a.Before, AfterPath: a.After, ConsoleTail: a.ConsoleTail, Output: a.Output, At: a.At, MS: a.MS, TargetRole: a.TargetRole, TargetName: a.TargetName, Box: a.Box, Reason: a.Reason, PageErrors: a.PageErrors})
	}
}

// appendRunLog keeps diagnostics from independent cleanup and expiry steps.
func (s *Store) appendRunLog(id, line string) error {
	path, err := s.path(id, "run.log")
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintln(f, line)
	return errors.Join(err, f.Close())
}
