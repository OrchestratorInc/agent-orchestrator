package telemetry

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// OptOutFile is the marker the desktop app writes when the user turns off
// telemetry in Settings. It lives in the data dir so the daemon, the desktop
// app and (via the identity route) the phone all read one switch.
const OptOutFile = "telemetry_opt_out"

var (
	cloudUserIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	githubLoginPattern = regexp.MustCompile(`^[A-Za-z0-9_.-]{1,64}$`)
)

// Identity is the daemon's live telemetry identity: the install ID plus the
// two optional person-level attributes. Safe for concurrent use.
type Identity struct {
	installID  string
	optOutPath string
	fresh      bool

	mu          sync.RWMutex
	cloudUserID string
	githubLogin string
}

// NewIdentity loads (or creates) the install ID under dataDir.
func NewIdentity(dataDir string) (*Identity, error) {
	id, err := loadOrCreateInstallID(dataDir)
	if err != nil {
		return nil, err
	}
	return &Identity{installID: id, optOutPath: filepath.Join(dataDir, OptOutFile), fresh: freshInstall(dataDir)}, nil
}

// FreshInstall reports whether this process is the first run of a brand new
// install, the one moment ao.app.installed is emitted.
func (i *Identity) FreshInstall() bool { return i.fresh }

// freshInstall is true when no event has ever been exported (the tenure file is
// written on the first one) and the install ID was created in the last day. The
// age check keeps an upgrade from a build that predates tenure from counting
// as a new install.
func freshInstall(dataDir string) bool {
	if _, err := os.Stat(filepath.Join(dataDir, tenureStateFile)); err == nil {
		return false
	}
	info, err := os.Stat(filepath.Join(dataDir, "telemetry_install_id"))
	return err == nil && time.Since(info.ModTime()) < 24*time.Hour
}

// OptedOut reports whether the user switched telemetry off.
// ponytail: one stat per exported event, cached nowhere; events are rate
// limited upstream, add an mtime cache if that ever stops being true.
func (i *Identity) OptedOut() bool {
	_, err := os.Stat(i.optOutPath)
	return err == nil
}

// SetCloudUserID implements ports.TelemetryIdentityStore.
func (i *Identity) SetCloudUserID(id string) bool {
	id = strings.TrimSpace(id)
	if id != "" && !cloudUserIDPattern.MatchString(id) {
		return false
	}
	i.mu.Lock()
	i.cloudUserID = id
	i.mu.Unlock()
	return true
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
// no user attributes at all.
func (i *Identity) Snapshot() ports.TelemetryIdentity {
	snap := ports.TelemetryIdentity{InstallID: i.installID, OptedOut: i.OptedOut()}
	if snap.OptedOut {
		return snap
	}
	i.mu.RLock()
	snap.CloudUserID, snap.GitHubLogin = i.cloudUserID, i.githubLogin
	i.mu.RUnlock()
	return snap
}
