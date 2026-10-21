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

// OptOutFile is the marker the desktop app writes when the user turns off
// telemetry in Settings. It lives in the data dir so the daemon, the desktop
// app and (via the identity route) the phone all read one switch.
const OptOutFile = "telemetry_opt_out"

const (
	installIDFile      = "telemetry_install_id"
	installedEventFile = "telemetry_installed_emitted"
	// boundUserFile remembers which AO Cloud user the install ID may be linked
	// to. The desktop identify() merges the install ID into that person for good,
	// so once the user changes or signs out the ID is retired.
	boundUserFile = "telemetry_bound_user"
)

var (
	cloudUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// Identity is the daemon's live telemetry identity: the install ID plus the
// two optional person-level attributes. Safe for concurrent use.
type Identity struct {
	dataDir    string
	optOutPath string
	fresh      bool

	// File operations are fields so tests can fail them.
	writeFile  func(path string, data []byte) error
	removeFile func(path string) error

	mu          sync.RWMutex
	installID   string
	cloudUserID string
	// persistFailed is set while the last SetCloudUserID could not persist.
	persistFailed bool
	githubLogin   string
}

// NewIdentity loads (or creates) the install ID under dataDir.
func NewIdentity(dataDir string) (*Identity, error) {
	id, err := loadOrCreateInstallID(dataDir)
	if err != nil {
		return nil, err
	}
	return &Identity{writeFile: writeFileAtomic, removeFile: os.Remove, dataDir: dataDir, installID: id, optOutPath: filepath.Join(dataDir, OptOutFile), fresh: freshInstall(dataDir)}, nil
}

// FreshInstall reports whether this process is the first run of a brand new
// install, the one moment ao.app.installed is emitted.
func (i *Identity) FreshInstall() bool { return i.fresh }

// MarkInstalledEvent reserves the one install event before it enters the
// best-effort queue. A restart cannot emit it again if the queued send is lost.
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

// freshInstall is true when the install event has not been reserved, no event
// has been exported, and the install ID was created in the last day. The age
// check keeps an upgrade from a build that predates tenure from counting as a
// new install.
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

// OptedOut reports whether the user switched telemetry off. Queue admission
// and each send attempt read the marker so an opt-out stops export without a
// cache delay.
func (i *Identity) OptedOut() bool {
	_, err := os.Stat(i.optOutPath)
	return err == nil
}

// InstallID is the current anonymous install ID. It changes when an account
// sign-out or switch retires the previous one.
func (i *Identity) InstallID() string {
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.installID
}

// SetCloudUserID implements ports.TelemetryIdentityStore. The install ID is
// aliased into the first account that signs in on the desktop and PostHog never
// splits it again, so a sign-out or a different account retires it for a fresh
// anonymous one. Without that, signed-out or next-account daemon events would
// keep resolving to the first account.
//
// The binding must be durable before any event is exported under it, so a failed
// write leaves the previous binding in place, returns the error, and suppresses
// remote export until a later call completes cleanly.
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
	err := i.bindLocked(id)
	i.persistFailed = err != nil
	return err
}

func (i *Identity) bindLocked(id string) error {
	boundPath := filepath.Join(i.dataDir, boundUserFile)
	bound, err := readTrimmed(boundPath)
	if err != nil {
		return fmt.Errorf("read telemetry bound user: %w", err)
	}
	if bound != "" && bound != id {
		next := "ins_" + uuid.NewString()
		if err := i.writeFile(filepath.Join(i.dataDir, installIDFile), []byte(next+"\n")); err != nil {
			return fmt.Errorf("retire telemetry install id: %w", err)
		}
		i.installID = next
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

// ExportSuppressed reports whether remote export must stop: the user opted out,
// or the identity state could not be persisted and an event could be attributed
// to an account the install ID is no longer allowed to represent.
func (i *Identity) ExportSuppressed() bool {
	if i.OptedOut() {
		return true
	}
	i.mu.RLock()
	defer i.mu.RUnlock()
	return i.persistFailed
}

// SetGitHubLogin implements ports.TelemetryIdentityStore. A login that does not
// look like one is dropped rather than exported.
func (i *Identity) SetGitHubLogin(login string) {
	login = strings.TrimSpace(login)
	if login != "" && !githubLoginPattern.MatchString(login) {
		return
	}
	i.mu.Lock()
	i.githubLogin = login
	i.mu.Unlock()
}

// Snapshot implements ports.TelemetryIdentityStore. Opted-out installs report
// no user attributes at all. An identity that could not be persisted reports the
// same way, so a paired phone resets and stops capturing until a later
// SetCloudUserID succeeds, instead of adopting an install ID that is still
// linked to the previous account.
func (i *Identity) Snapshot() ports.TelemetryIdentity {
	optedOut := i.OptedOut()
	i.mu.RLock()
	defer i.mu.RUnlock()
	snap := ports.TelemetryIdentity{InstallID: i.installID, OptedOut: optedOut || i.persistFailed}
	if !snap.OptedOut {
		snap.CloudUserID, snap.GitHubLogin = i.cloudUserID, i.githubLogin
	}
	return snap
}

// eventAttributes avoids another marker read while constructing an export.
// The sender checks ExportSuppressed immediately before each HTTP attempt.
func (i *Identity) eventAttributes() (cloudUserID, githubLogin string) {
	i.mu.RLock()
	defer i.mu.RUnlock()
	if i.persistFailed {
		return "", ""
	}
	return i.cloudUserID, i.githubLogin
}
