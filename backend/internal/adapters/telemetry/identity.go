package telemetry

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const OptOutFile = "telemetry_opt_out"

const (
	installIDFile      = "telemetry_install_id"
	anonymousIDFile    = "telemetry_anonymous_id"
	installedEventFile = "telemetry_installed_emitted"
	boundUserFile      = "telemetry_bound_user"
)

var (
	cloudUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

type Identity struct {
	dataDir    string
	optOutPath string
	fresh      bool

	writeFile  func(path string, data []byte) error
	removeFile func(path string) error

	mu             sync.RWMutex
	installID      string
	anonymousID    string
	onRetired      func()
	cloudUserID    string
	cloudUserKnown bool
	persistFailed  bool
	githubLogin    string
}

func NewIdentity(dataDir string) (*Identity, error) {
	id, err := loadOrCreateInstallID(dataDir)
	if err != nil {
		return nil, err
	}
	anon, err := readTrimmed(filepath.Join(dataDir, anonymousIDFile))
	if err != nil {
		return nil, err
	}
	if anon == "" {
		anon = id
	}
	bound, err := readTrimmed(filepath.Join(dataDir, boundUserFile))
	if err != nil {
		return nil, err
	}
	if bound != "" && anon == id {
		anon = "anon_" + uuid.NewString()
		if err := writeFileAtomic(filepath.Join(dataDir, anonymousIDFile), []byte(anon+"\n")); err != nil {
			return nil, fmt.Errorf("record telemetry anonymous id: %w", err)
		}
	}
	ident := &Identity{writeFile: writeFileAtomic, removeFile: os.Remove, dataDir: dataDir, installID: id, anonymousID: anon, optOutPath: filepath.Join(dataDir, OptOutFile), fresh: freshInstall(dataDir)}
	if cloudUserIDPattern.MatchString(bound) {
		ident.cloudUserID, ident.cloudUserKnown = bound, true
	}
	return ident, nil
}

func (i *Identity) FreshInstall() bool { return i.fresh }

func (i *Identity) OnInstallRetired(fn func()) {
	if i == nil {
		return
	}
	i.mu.Lock()
	i.onRetired = fn
	i.mu.Unlock()
}

func (i *Identity) MarkInstalledEvent(ctx context.Context) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if !i.fresh || i.OptedOut() {
		return false, nil
	}
	file, err := os.OpenFile(filepath.Join(i.dataDir, installedEventFile), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if os.IsExist(err) {
		i.fresh = false
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("mark telemetry install event: %w", err)
	}
	if err := file.Close(); err != nil {
		return false, fmt.Errorf("close telemetry install event marker: %w", err)
	}
	i.fresh = false
	return true, nil
}

func freshInstall(dataDir string) bool {
	if _, err := os.Stat(filepath.Join(dataDir, installedEventFile)); !os.IsNotExist(err) {
		return false
	}
	if _, err := os.Stat(filepath.Join(dataDir, tenureStateFile)); err == nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dataDir, installIDFile))
	return err == nil && time.Since(info.ModTime()) < 24*time.Hour
}

func (i *Identity) OptedOut() bool {
	_, err := os.Stat(i.optOutPath)
	return err == nil
}

func (i *Identity) InstallID() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.installID
}

func (i *Identity) DaysSinceInstall(now time.Time) (int, bool) {
	info, err := os.Stat(filepath.Join(i.dataDir, installIDFile))
	if err != nil || info.ModTime().After(now) {
		return 0, false
	}
	return int(now.UTC().Sub(info.ModTime().UTC()).Hours() / 24), true
}

func (i *Identity) AnonymousID() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.anonymousID
}

func (i *Identity) SetCloudUserID(ctx context.Context, id string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	id = strings.TrimSpace(id)
	if id != "" && !cloudUserIDPattern.MatchString(id) {
		return ports.ErrInvalidCloudUserID
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	i.cloudUserID = id
	i.cloudUserKnown = true
	err := i.bindLocked(id)
	i.persistFailed = err != nil
	return err
}

func (i *Identity) bindLocked(id string) error {
	if anon, err := readTrimmed(filepath.Join(i.dataDir, anonymousIDFile)); err == nil && anon != "" {
		i.anonymousID = anon
	}
	boundPath := filepath.Join(i.dataDir, boundUserFile)
	bound, err := readTrimmed(boundPath)
	if err != nil {
		return fmt.Errorf("read telemetry bound user: %w", err)
	}
	if bound != "" && bound != id {
		next := "anon_" + uuid.NewString()
		if err := i.writeFile(filepath.Join(i.dataDir, anonymousIDFile), []byte(next+"\n")); err != nil {
			return fmt.Errorf("retire telemetry anonymous id: %w", err)
		}
		i.anonymousID = next
		_ = os.Remove(filepath.Join(i.dataDir, "telemetry", "cli-usage-daily.json"))
		if i.onRetired != nil {
			retired := i.onRetired
			i.mu.Unlock()
			retired()
			i.mu.Lock()
		}
	}
	switch {
	case id == "" && bound != "":
		if err := i.removeFile(boundPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("clear telemetry bound user: %w", err)
		}
	case id != "" && bound != id:
		if err := i.writeFile(boundPath, []byte(id+"\n")); err != nil {
			return fmt.Errorf("record telemetry bound user: %w", err)
		}
	}
	return nil
}

func readTrimmed(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	return strings.TrimSpace(string(b)), err
}

func (i *Identity) ExportSuppressed() bool {
	if i.OptedOut() {
		return true
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.persistFailed
}

func (i *Identity) SetGitHubLogin(login string) {
	login = strings.TrimSpace(login)
	if login != "" && !githubLoginPattern.MatchString(login) {
		return
	}
	i.mu.Lock()
	i.githubLogin = login
	i.mu.Unlock()
}

func (i *Identity) Snapshot() ports.TelemetryIdentity {
	optedOut := i.OptedOut()
	i.mu.RLock()
	defer i.mu.RUnlock()
	snap := ports.TelemetryIdentity{InstallID: i.installID, AnonymousID: i.anonymousID, OptedOut: optedOut || i.persistFailed}
	if !snap.OptedOut {
		snap.CloudUserID, snap.GitHubLogin = i.cloudUserID, i.githubLogin
		snap.CloudUserKnown = i.cloudUserKnown
	}
	return snap
}

func (i *Identity) eventAttributes() (cloudUserID, githubLogin string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.persistFailed {
		return "", ""
	}
	return i.cloudUserID, i.githubLogin
}
