package litmus

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// Store keeps each run as plain files under <dataDir>/litmus/runs/<runID>/:
// run.json, checks.json, actions.jsonl, receipts/, context-pack.md, verdict.md.
type Store struct{ root string }

// NewStore returns a store rooted under dataDir. Nothing is created until a run is saved.
func NewStore(dataDir string) *Store {
	return &Store{root: filepath.Join(dataDir, "litmus", "runs")}
}

// dataDir is the AO data dir the store lives under: <dataDir>/litmus/runs.
func (s *Store) dataDir() string { return filepath.Dir(filepath.Dir(s.root)) }

// reposDir holds the clones of one repo, one dir per SHA: <dataDir>/litmus/repos/<owner>__<name>.
func (s *Store) reposDir(repo string) string {
	return filepath.Join(filepath.Dir(s.root), "repos", strings.ReplaceAll(repo, "/", "__"))
}

// legDir is where one leg's private state lives: <runDir>/<leg>.
func (s *Store) legDir(runID, leg string) string { return filepath.Join(s.root, runID, leg) }

func (s *Store) path(id string, elem ...string) (string, error) {
	if id == "" || id == "." || id == ".." || filepath.Base(id) != id {
		return "", apierr.Invalid("TESTING_ID_INVALID", fmt.Sprintf("invalid run id %q", id), nil)
	}
	return filepath.Join(append([]string{s.root, id}, elem...)...), nil
}

// Save writes run.json atomically, creating the run directory and receipts/ on first save.
func (s *Store) Save(run Run) error {
	dir, err := s.path(run.ID, "receipts")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return err
	}
	return s.put(run.ID, "run.json", run)
}

// Get loads one run. A missing run wraps fs.ErrNotExist.
func (s *Store) Get(id string) (run Run, err error) {
	err = s.read(id, "run.json", &run)
	return run, err
}

// List returns every readable run, oldest first.
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

// SaveChecks replaces checks.json atomically.
func (s *Store) SaveChecks(id string, checks []Check) error { return s.put(id, "checks.json", checks) }

// Checks loads checks.json; a run with no checks yet yields nil.
func (s *Store) Checks(id string) (checks []Check, err error) {
	if err = s.read(id, "checks.json", &checks); errors.Is(err, fs.ErrNotExist) {
		err = nil
	}
	return checks, err
}

// WriteFile atomically writes a plain file such as verdict.md into the run directory.
func (s *Store) WriteFile(id, name string, data []byte) error {
	path, err := s.path(id, name)
	if err != nil || filepath.Base(name) != name {
		return fmt.Errorf("invalid run file %q/%q", id, name)
	}
	return writeAtomic(path, data)
}

func (s *Store) put(id, name string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return s.WriteFile(id, name, data)
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

// writeAtomic writes via a temp file in the same directory and renames it into
// place, so readers see the old or the new content, never a partial file.
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

// offPath marks a session whose run must not post its comment or its evidence: <dataDir>/litmus/<kind>-off/<sessionID>.
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

// commentMode is what intake gives the session's run: off when start asked for no comment, else post.
func (s *Store) commentMode(sessionID string) string {
	if path, err := s.offPath("comment", sessionID); err == nil && fileExists(path) {
		return CommentOff
	}
	return CommentPost
}

// evidenceMode is the same for the evidence files: off when start asked for none, else post.
func (s *Store) evidenceMode(sessionID string) string {
	if path, err := s.offPath("evidence", sessionID); err == nil && fileExists(path) {
		return EvidenceOff
	}
	return EvidencePost
}
