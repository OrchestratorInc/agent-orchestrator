// Package reviewgateway prepares AO-owned runtime directories and immutable
// task manifests used by interactive reviewer TUIs.
package reviewgateway

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

const manifestVersion = 1

var (
	safeID    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	commitSHA = regexp.MustCompile(`^[0-9a-fA-F]{7,64}$`)
)

// Task is one immutable review capability.
type Task struct {
	RunID          string `json:"runId"`
	PRURL          string `json:"prUrl"`
	TargetSHA      string `json:"targetSha"`
	TaskPromptFile string `json:"taskPromptFile"`
}

// Manifest authorizes a reviewer process for one worker and an exact set of
// review runs. WorkspacePath is host-side gateway state and is never intended
// to be exposed as the TUI's working directory.
type Manifest struct {
	Version         int              `json:"version"`
	ReviewerID      string           `json:"reviewerId"`
	WorkerSessionID domain.SessionID `json:"workerSessionId"`
	WorkspacePath   string           `json:"workspacePath"`
	TaskPromptRoot  string           `json:"taskPromptRoot"`
	Tasks           []Task           `json:"tasks"`
}

// Environment is the neutral AO-owned filesystem presented to a reviewer.
// The source checkout is intentionally absent.
type Environment struct {
	DataDir          string
	Root             string
	WorkingDirectory string
	ConfigRoot       string
	StateRoot        string
	CacheRoot        string
	TempRoot         string
	ManifestPath     string
}

// TUIEnvironment returns provider-neutral directory overrides for an adapter's
// RuntimeConfig.Env. Provider-specific adapters may add only documented flags;
// the eventual OS sandbox remains responsible for excluding inherited host state.
func (e Environment) TUIEnvironment() map[string]string {
	return map[string]string{
		"HOME": e.ConfigRoot, "XDG_CONFIG_HOME": e.ConfigRoot,
		"XDG_STATE_HOME": e.StateRoot, "XDG_CACHE_HOME": e.CacheRoot,
		"TMPDIR": e.TempRoot, "TEMP": e.TempRoot, "TMP": e.TempRoot,
	}
}

// PrepareHostTrustedEnvironment creates AO-owned discovery and state roots for
// an experimental host-trusted reviewer. It limits where the CLI stores its own
// files but intentionally does not claim process, filesystem, or network
// containment.
func PrepareHostTrustedEnvironment(dataDir, reviewerID string) (Environment, error) {
	if strings.TrimSpace(dataDir) == "" || !filepath.IsAbs(dataDir) {
		return Environment{}, errors.New("review gateway: absolute Orchestrator data directory is required")
	}
	if !safeID.MatchString(reviewerID) {
		return Environment{}, errors.New("review gateway: invalid reviewer id")
	}
	root := filepath.Join(dataDir, "reviewer-runtime", reviewerID)
	env := Environment{
		DataDir: dataDir, Root: root,
		WorkingDirectory: filepath.Join(root, "workspace"),
		ConfigRoot:       filepath.Join(root, "config"), StateRoot: filepath.Join(root, "state"),
		CacheRoot: filepath.Join(root, "cache"), TempRoot: filepath.Join(root, "tmp"),
	}
	for _, dir := range []string{env.Root, env.WorkingDirectory, env.ConfigRoot, env.StateRoot, env.CacheRoot, env.TempRoot} {
		if err := ensurePrivateDir(dir); err != nil {
			return Environment{}, err
		}
	}
	return env, nil
}

// PrepareEnvironment creates a private reviewer root under AO_DATA_DIR and an
// immutable, content-addressed authorization manifest. It never creates files
// in the project checkout.
func PrepareEnvironment(dataDir string, manifest Manifest) (Environment, error) {
	if strings.TrimSpace(dataDir) == "" {
		return Environment{}, errors.New("review gateway: Orchestrator data directory is required")
	}
	if !filepath.IsAbs(dataDir) {
		return Environment{}, errors.New("review gateway: Orchestrator data directory must be absolute")
	}
	if err := validateManifest(&manifest); err != nil {
		return Environment{}, err
	}
	root := filepath.Join(dataDir, "reviewer-runtime", manifest.ReviewerID)
	env := Environment{
		DataDir:          dataDir,
		Root:             root,
		WorkingDirectory: filepath.Join(root, "workspace"),
		ConfigRoot:       filepath.Join(root, "config"),
		StateRoot:        filepath.Join(root, "state"),
		CacheRoot:        filepath.Join(root, "cache"),
		TempRoot:         filepath.Join(root, "tmp"),
	}
	for _, dir := range []string{filepath.Join(dataDir, "reviewer-runtime"), env.Root, env.WorkingDirectory, env.ConfigRoot, env.StateRoot, env.CacheRoot, env.TempRoot, filepath.Join(root, "manifests"), filepath.Join(root, "disabled-git-hooks")} {
		if err := ensurePrivateDir(dir); err != nil {
			return Environment{}, err
		}
	}
	raw, err := json.Marshal(manifest)
	if err != nil {
		return Environment{}, fmt.Errorf("review gateway: encode manifest: %w", err)
	}
	sum := sha256.Sum256(raw)
	env.ManifestPath = filepath.Join(root, "manifests", hex.EncodeToString(sum[:])+".json")
	if prior, readErr := os.ReadFile(env.ManifestPath); readErr == nil {
		if !bytes.Equal(prior, raw) {
			return Environment{}, errors.New("review gateway: content-addressed manifest collision")
		}
		return env, nil
	} else if !errors.Is(readErr, os.ErrNotExist) {
		return Environment{}, fmt.Errorf("review gateway: inspect manifest: %w", readErr)
	}
	f, err := os.OpenFile(env.ManifestPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return Environment{}, fmt.Errorf("review gateway: create manifest: %w", err)
	}
	if _, err = f.Write(raw); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return Environment{}, fmt.Errorf("review gateway: write manifest: %w", err)
	}
	if closeErr != nil {
		return Environment{}, fmt.Errorf("review gateway: close manifest: %w", closeErr)
	}
	return env, nil
}

func validateManifest(manifest *Manifest) error {
	if manifest.Version != 0 && manifest.Version != manifestVersion {
		return errors.New("review gateway: unsupported manifest version")
	}
	manifest.Version = manifestVersion
	if !safeID.MatchString(manifest.ReviewerID) || strings.TrimSpace(string(manifest.WorkerSessionID)) == "" {
		return errors.New("review gateway: invalid reviewer or worker id")
	}
	if !filepath.IsAbs(manifest.WorkspacePath) || !filepath.IsAbs(manifest.TaskPromptRoot) || len(manifest.Tasks) == 0 {
		return errors.New("review gateway: absolute workspace, prompt root, and tasks are required")
	}
	seen := make(map[string]bool, len(manifest.Tasks))
	for _, task := range manifest.Tasks {
		if !safeID.MatchString(task.RunID) || seen[task.RunID] || !commitSHA.MatchString(task.TargetSHA) {
			return errors.New("review gateway: invalid or duplicate task identity")
		}
		if _, _, err := parsePRURL(task.PRURL); err != nil {
			return err
		}
		prompt, err := filepath.Abs(task.TaskPromptFile)
		if err != nil || !pathWithin(manifest.TaskPromptRoot, prompt) {
			return errors.New("review gateway: task prompt is outside the Orchestrator prompt root")
		}
		seen[task.RunID] = true
	}
	return nil
}

func pathWithin(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func parsePRURL(raw string) (string, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.RawQuery != "" || u.Fragment != "" {
		return "", "", errors.New("review gateway: PR URL must be an https github.com pull URL")
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 4 || parts[2] != "pull" || !validGitHubSegment(parts[0]) || !validGitHubSegment(parts[1]) {
		return "", "", errors.New("review gateway: invalid GitHub pull URL")
	}
	n, err := strconv.Atoi(parts[3])
	if err != nil || n < 1 {
		return "", "", errors.New("review gateway: invalid pull request number")
	}
	return parts[0] + "/" + parts[1], strconv.Itoa(n), nil
}

func validGitHubSegment(segment string) bool {
	if segment == "." || segment == ".." || segment == "" {
		return false
	}
	for _, r := range segment {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' && r != '_' && r != '.' {
			return false
		}
	}
	return true
}

func ensurePrivateDir(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("review gateway: create private directory: %w", err)
	}
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("review gateway: private path is not a real directory")
	}
	if err := os.Chmod(path, 0o700); err != nil { //nolint:gosec // directories require execute permission and remain owner-only
		return fmt.Errorf("review gateway: secure private directory: %w", err)
	}
	return nil
}
