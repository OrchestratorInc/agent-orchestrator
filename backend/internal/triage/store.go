package triage

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

type runStore struct{ root string }

func openRunStore(dataDir string) (*runStore, error) {
	if strings.TrimSpace(dataDir) == "" {
		return nil, errors.New("triage data directory is required")
	}
	s := &runStore{root: filepath.Join(dataDir, "reports", "triage")}
	if err := privateDir(s.root); err != nil {
		return nil, err
	}
	if err := privateDir(filepath.Join(s.root, "runs")); err != nil {
		return nil, err
	}
	return s, nil
}

func privateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("triage directory must be a private directory")
	}
	return os.Chmod(path, 0o700) //nolint:gosec // Owner-only directory needs traversal permission.
}

var runIDPattern = regexp.MustCompile(`^[a-zA-Z0-9_-]{1,128}$`)

func validRunID(id string) bool { return runIDPattern.MatchString(id) }

func (s *runStore) runDir(id string) (string, error) {
	return RunDir(filepath.Dir(filepath.Dir(s.root)), id)
}

// RunDir returns the private run directory without loading config or looking up
// a daemon. The host creates it before invoking the conversation dependency.
func RunDir(dataDir, id string) (string, error) {
	if strings.TrimSpace(dataDir) == "" {
		return "", errors.New("triage data directory is required")
	}
	if !validRunID(id) {
		return "", errors.New("invalid triage run id")
	}
	dir := filepath.Join(dataDir, "reports", "triage", "runs", id)
	if info, err := os.Lstat(dir); err == nil && (!info.IsDir() || info.Mode()&os.ModeSymlink != 0) {
		return "", errors.New("invalid triage run directory")
	} else if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	return dir, nil
}

// sanitizeJSON walks values rather than redacting JSON syntax. Redaction must
// never turn a quote or a backslash in provider text into a broken manifest.
func sanitizeJSON(value any) ([]byte, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	var decoded any
	if err := decodeJSON(raw, &decoded, false); err != nil {
		return nil, err
	}
	return json.MarshalIndent(redactValue(decoded), "", "  ")
}

func atomicPrivateJSON(path string, value any) error {
	raw, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(path, append(raw, '\n'), 0o600)
}

func (s *runStore) save(run Run) error {
	dir, err := s.runDir(run.ID)
	if err != nil {
		return err
	}
	if err := privateDir(dir); err != nil {
		return err
	}
	run.PendingInput, run.TerminalReplay, run.TerminalSequence = nil, "", 0
	raw, err := sanitizeJSON(run)
	if err != nil {
		return err
	}
	return hookutil.AtomicWriteFile(filepath.Join(dir, "manifest.json"), append(raw, '\n'), 0o600)
}

// Bound every retained manifest read and reject symlinks and special files.
func readPrivateJSON(path string, limit int64, value any) error {
	raw, err := readPrivateBytes(path, limit)
	if err != nil {
		return err
	}
	return decodeJSON(raw, value, false)
}

func (s *runStore) get(id string) (Run, error) {
	dir, err := s.runDir(id)
	if err != nil {
		return Run{}, err
	}
	var run Run
	if err := readPrivateJSON(filepath.Join(dir, "manifest.json"), 2<<20, &run); err != nil {
		return Run{}, fmt.Errorf("read triage report: %w", err)
	}
	if run.ID != id {
		return Run{}, errors.New("triage manifest has a different run id")
	}
	if run.Launch != nil && (run.Request.PreparationID != id || run.Launch.ClientRequestID != "triage-"+id || len(run.Launch.Attachments) != 2) {
		return Run{}, errors.New("invalid saved session launch")
	}
	return run, nil
}

func (s *runStore) list() ([]Run, error) {
	entries, err := os.ReadDir(filepath.Join(s.root, "runs"))
	if err != nil {
		return nil, err
	}
	runs := []Run{}
	for _, entry := range entries {
		if !entry.IsDir() || !validRunID(entry.Name()) {
			continue
		}
		run, err := s.get(entry.Name())
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, err
		}
		runs = append(runs, run)
	}
	sort.Slice(runs, func(i, j int) bool { return runs[i].CreatedAt.After(runs[j].CreatedAt) })
	return runs, nil
}

var errRunOwned = errors.New("a triage investigation is already running")

// Each contender publishes a unique claim before checking the other claims.
// Two contenders cannot both observe an empty set. A stale claim has a unique
// name, so removing it cannot unlink a replacement owner's lock. A simultaneous
// collision may reject both starts, but it never launches two providers.
func claim(path string) (func(), error) {
	if err := privateDir(path); err != nil {
		return nil, err
	}
	slot, err := os.MkdirTemp(path, strconv.Itoa(os.Getpid())+"-")
	if err != nil {
		return nil, err
	}
	release := func() { _ = os.Remove(slot) }
	entries, err := os.ReadDir(path)
	if err != nil {
		release()
		return nil, err
	}
	for _, entry := range entries {
		otherPath := filepath.Join(path, entry.Name())
		if otherPath == slot {
			continue
		}
		pidText, _, _ := strings.Cut(entry.Name(), "-")
		pid, err := strconv.Atoi(pidText)
		if err != nil || pid <= 0 || !entry.IsDir() || processalive.Alive(pid) {
			release()
			return nil, errRunOwned
		}
		if err := os.Remove(otherPath); err != nil && !errors.Is(err, os.ErrNotExist) {
			release()
			return nil, errRunOwned
		}
	}
	return release, nil
}

func runIsActive(state string) bool {
	switch state {
	case "collecting", "prepared", "starting", "dispatching", "running", "working", "needs_input":
		return true
	default:
		return false
	}
}

func (s *runStore) recoverInterrupted(ctx context.Context, cleanup func(context.Context, Run) error) error {
	runs, err := s.list()
	if err != nil {
		return err
	}
	for _, run := range runs {
		// The ordinary session lifecycle owns external executions. In particular,
		// never reconcile or terminate its native provider after helper restart.
		if run.Execution == "session" && !run.Preparation {
			continue
		}
		if !runIsActive(run.State) {
			continue
		}
		if cleanup != nil && run.Execution != "session" {
			cleanupCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
			cleanupErr := cleanup(cleanupCtx, run)
			cancel()
			if cleanupErr != nil {
				run.Error = strings.TrimSpace(run.Error + "\nProvider cleanup incomplete: " + cleanupErr.Error())
			}
		}
		run.State = "interrupted"
		run.Activity = "Investigation interrupted. The saved report is available."
		run.PendingInput = nil
		if err := s.save(run); err != nil {
			return err
		}
	}
	return nil
}

func newMessageID(sequence int) string { return "message-" + strconv.Itoa(sequence) }
