package mobilebridge

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

const lidHelperVersion = 2
const lidHelperLabel = "com.aoagents.mobile-wake"

var errLidHelperUpgrade = errors.New("registered wake helper is incompatible; open the lid, turn closed-lid wake off in every AO instance and wait for recovery to finish, then run in Terminal: launchctl bootout gui/$(id -u)/com.aoagents.mobile-wake; turn closed-lid wake on again")

// LidAwake is a lease client for the independent, launchd-owned coordinator.
// Default-data AO instances for this login share ~/.ao/data/wake. Custom data
// roots cannot enable host control, which would break isolation.
type LidAwake struct {
	mu                    sync.Mutex
	transitionMu          sync.Mutex
	execute               func(context.Context, string, ...string) ([]byte, error)
	binary, directory, id string
	pid                   int
	started, sequence     uint64
	supported             bool
	reason                string
	cancel                context.CancelFunc
	done                  chan struct{}
}

type lidLease struct {
	ID        string  `json:"id"`
	PID       int     `json:"pid"`
	Started   uint64  `json:"started"`
	Sequence  uint64  `json:"sequence"`
	WrittenAt float64 `json:"writtenAt"`
}

type lidStatus struct {
	Version   int               `json:"version"`
	UpdatedAt float64           `json:"updatedAt"`
	Active    bool              `json:"active"`
	Reason    string            `json:"reason"`
	Leases    []string          `json:"leases"`
	Blocked   map[string]string `json:"blocked"`
}

// NewLidAwake probes without changing power state or registering a job. The
// helper is optional for CLI-only/non-desktop installations.
func NewLidAwake(pid int, binary, dataDir string) *LidAwake {
	l := &LidAwake{pid: pid, binary: binary, reason: "helper_missing"}
	home, err := os.UserHomeDir()
	if err != nil {
		return l
	}
	// Host power is global. Scratch/custom data roots must not install or
	// join the production coordinator; independent roots cannot own that bit.
	if filepath.Clean(dataDir) != filepath.Join(home, ".ao", "data") {
		l.reason = "isolated_data_dir"
		return l
	}
	if runtime.GOOS != "darwin" || runtime.GOARCH != "arm64" {
		l.reason = "unsupported"
		return l
	}
	if binary == "" || !filepath.IsAbs(binary) {
		return l
	}
	l.directory = filepath.Join(dataDir, "wake")
	ctx, cancel := context.WithTimeout(context.Background(), processProbeTimeout)
	defer cancel()
	out, err := aoprocess.CommandContext(ctx, binary, "probe").Output()
	if err != nil {
		l.reason = "native_failed"
		return l
	}
	var probe struct {
		Supported bool   `json:"supported"`
		Started   uint64 `json:"started"`
		Version   int    `json:"version"`
	}
	if json.Unmarshal(out, &probe) != nil || probe.Version != lidHelperVersion || probe.Started == 0 {
		l.reason = "native_failed"
		return l
	}
	l.supported, l.started = probe.Supported, probe.Started
	if !l.supported {
		l.reason = "unsupported"
		return l
	}
	l.reason = ""
	return l
}

// Status separates a saved request from the coordinator's fresh acknowledgement.
func (l *LidAwake) Status() (supported, active bool, reason string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.supported {
		return false, false, l.reason
	}
	if l.id == "" || l.reason == "helper_upgrade_required" {
		return true, false, l.reason
	}
	st, err := l.readStatus()
	if err != nil {
		return true, false, "helper_unavailable"
	}
	if blocked := st.Blocked[l.id]; blocked != "" {
		return true, false, blocked
	}
	for _, id := range st.Leases {
		if id == l.id {
			return true, st.Active, st.Reason
		}
	}
	if st.Reason != "" {
		return true, false, st.Reason
	}
	if l.cancel == nil {
		l.id, l.reason = "", ""
		return true, false, ""
	}
	return true, false, "starting"
}

func (l *LidAwake) readStatus() (lidStatus, error) {
	var st lidStatus
	file, err := os.Open(filepath.Join(l.directory, "status.json"))
	if err != nil {
		return st, err
	}
	defer func() { _ = file.Close() }()
	data, err := io.ReadAll(io.LimitReader(file, 65537))
	if err != nil {
		return st, err
	}
	if len(data) > 65536 {
		return st, errors.New("oversized wake status")
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return st, err
	}
	age := float64(time.Now().UnixMilli())/1000 - st.UpdatedAt
	if st.Version != lidHelperVersion || age < -5 || age > 30 {
		return st, errors.New("stale wake status")
	}
	return st, nil
}

// Start requires explicit user opt-in. Registration never runs on boot merely
// because the helper binary exists. A restored preference can renew an already
// registered helper, but cannot silently install one.
func (l *LidAwake) Start(allowSetup bool) error {
	l.transitionMu.Lock()
	defer l.transitionMu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.supported {
		return errors.New("closed-lid wakefulness is unavailable on this host")
	}
	if l.cancel != nil {
		return nil
	}
	if !allowSetup {
		if _, err := os.Stat(filepath.Join(l.directory, "agent.plist")); err != nil {
			l.reason = "setup_needed"
			return nil
		}
	}
	if err := l.setup(); err != nil {
		l.reason = "setup_failed"
		if errors.Is(err, errLidHelperUpgrade) {
			l.reason = "helper_upgrade_required"
		}
		return err
	}
	l.id = uuid.NewString()
	l.sequence = 0
	if err := l.renew(); err != nil {
		l.reason = "lease_failed"
		_ = os.Remove(filepath.Join(l.directory, "leases", l.id+".json"))
		return err
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	l.cancel, l.done, l.reason = cancel, done, ""
	go l.run(ctx, done)
	return nil
}

func (l *LidAwake) renew() error {
	l.sequence++
	lease := lidLease{l.id, l.pid, l.started, l.sequence, float64(time.Now().UnixMilli()) / 1000}
	data, err := json.Marshal(lease)
	if err != nil {
		return err
	}
	return writeWakeFile(filepath.Join(l.directory, "leases", l.id+".json"), data, 0o600)
}

func (l *LidAwake) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			l.mu.Lock()
			// Stop does not clear cancel until this goroutine has returned.
			st, err := l.readStatus()
			if err == nil && st.Blocked[l.id] != "" {
				// Keep the same lease identity while latched. Only an explicit off/on
				// generates a new identity and permits the native coordinator to rearm.
				l.reason = st.Blocked[l.id]
			}
			if err = l.renew(); err != nil {
				l.reason = "lease_failed"
			}
			l.mu.Unlock()
		}
	}
}

// Stop withdraws this daemon's lease and waits for native cleanup acknowledgement.
func (l *LidAwake) Stop() {
	l.transitionMu.Lock()
	defer l.transitionMu.Unlock()
	l.mu.Lock()
	cancel, done, id := l.cancel, l.done, l.id
	l.mu.Unlock()
	if cancel != nil {
		cancel()
		<-done
	}
	l.mu.Lock()
	l.cancel, l.done = nil, nil
	l.mu.Unlock()
	if id == "" {
		return
	}
	reason := "recovery_failed"
	if err := os.Remove(filepath.Join(l.directory, "leases", id+".json")); err != nil && !os.IsNotExist(err) {
		reason = "lease_failed"
	} else {
		// Poll without the status mutex. Only lifecycle operations are serialized.
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			st, err := l.readStatus()
			if err == nil {
				owned := false
				for _, leaseID := range st.Leases {
					if leaseID == id {
						owned = true
					}
				}
				if !owned && st.Reason != "recovery_failed" {
					reason = ""
					break
				}
			}
			time.Sleep(100 * time.Millisecond)
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.id == id {
		l.reason = reason
		if reason == "" {
			l.id = ""
		}
	}
}

func secureWakeDirectory(directory string) error {
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	st, err := os.Lstat(directory)
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode().Perm()&0o077 != 0 {
		return errors.New("wake directory must be private and not a symlink")
	}
	return nil
}

func writeWakeFile(name string, data []byte, mode os.FileMode) error {
	f, err := os.CreateTemp(filepath.Dir(name), ".wake-*")
	if err != nil {
		return err
	}
	temporary := f.Name()
	defer func() { _ = os.Remove(temporary) }()
	if err = f.Chmod(mode); err == nil {
		_, err = f.Write(data)
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(temporary, name); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(name))
	if err != nil {
		return err
	}
	defer func() { _ = parent.Close() }()
	return parent.Sync()
}

func (l *LidAwake) setup() error {
	for _, directory := range []string{l.directory, filepath.Join(l.directory, "leases")} {
		if err := secureWakeDirectory(directory); err != nil {
			return err
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	unlock, err := lockWakeSetup(ctx, filepath.Join(l.directory, "setup.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	domain := fmt.Sprintf("gui/%d", os.Getuid())
	if _, err := l.command(ctx, "/bin/launchctl", "print", domain+"/"+lidHelperLabel); err == nil {
		// Never boot out a registered coordinator: another build may hold a
		// live lease. Reuse only its compatible protocol, including when idle.
		return l.checkRegisteredHelper(ctx)
	}
	data, err := os.ReadFile(l.binary)
	if err != nil {
		return err
	}
	digest := sha256.Sum256(data)
	// Immutable version paths let an old registered job recover safely across an
	// app replacement; no launchd job points into a disappearing .app bundle.
	binary := filepath.Join(l.directory, "ao-wake-"+hex.EncodeToString(digest[:12]))
	if err := writeWakeFile(binary, data, 0o700); err != nil {
		return err
	}
	escape := func(s string) string {
		var b strings.Builder
		_ = xml.EscapeText(&b, []byte(s))
		return b.String()
	}
	plist := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict><key>Label</key><string>%s</string><key>ProgramArguments</key><array><string>%s</string><string>serve</string></array><key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>2</integer><key>ProcessType</key><string>Standard</string></dict></plist>
`, lidHelperLabel, escape(binary)))
	name := filepath.Join(l.directory, "agent.plist")
	if err := writeWakeFile(name, plist, 0o600); err != nil {
		return err
	}
	if _, err := l.command(ctx, "/bin/launchctl", "bootstrap", domain, name); err != nil {
		return err
	}
	// No previous job was registered, and setup is serialized between clients.
	// Keep the newly registered executable; remove only obsolete helper copies.
	entries, err := os.ReadDir(l.directory)
	if err == nil {
		for _, entry := range entries {
			candidate := filepath.Join(l.directory, entry.Name())
			if !entry.IsDir() && strings.HasPrefix(entry.Name(), "ao-wake-") && candidate != binary {
				_ = os.Remove(candidate)
			}
		}
	}
	return nil
}

func (l *LidAwake) command(ctx context.Context, binary string, args ...string) ([]byte, error) {
	if l.execute != nil {
		return l.execute(ctx, binary, args...)
	}
	return aoprocess.CommandContext(ctx, binary, args...).Output()
}

func (l *LidAwake) checkRegisteredHelper(ctx context.Context) error {
	data, err := os.ReadFile(filepath.Join(l.directory, "agent.plist"))
	if err != nil {
		if os.IsNotExist(err) {
			// The label may still belong to a coordinator using the legacy
			// ~/.ao/wake layout. Never replace it while it could own leases.
			return fmt.Errorf("registered wake helper metadata is missing: %w", errLidHelperUpgrade)
		}
		return err
	}
	var plist struct {
		Arguments []struct {
			Strings []string `xml:"string"`
		} `xml:"dict>array"`
	}
	if err := xml.Unmarshal(data, &plist); err != nil {
		return err
	}
	if len(plist.Arguments) != 1 || len(plist.Arguments[0].Strings) != 2 {
		return errors.New("invalid wake helper registration")
	}
	binary := plist.Arguments[0].Strings[0]
	if filepath.Dir(binary) != l.directory || !strings.HasPrefix(filepath.Base(binary), "ao-wake-") || plist.Arguments[0].Strings[1] != "serve" {
		return errors.New("invalid wake helper registration")
	}
	out, err := l.command(ctx, binary, "probe")
	if err != nil {
		return err
	}
	var probe struct {
		Version   int  `json:"version"`
		Supported bool `json:"supported"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return err
	}
	if probe.Version != lidHelperVersion || !probe.Supported {
		return errLidHelperUpgrade
	}
	return nil
}

// Setup registers the fixed recovery executable without acquiring wake ownership.
func (l *LidAwake) Setup() error {
	l.transitionMu.Lock()
	defer l.transitionMu.Unlock()
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.supported {
		return errors.New("closed-lid wakefulness is unavailable on this host")
	}
	if err := l.setup(); err != nil {
		l.reason = "setup_failed"
		if errors.Is(err, errLidHelperUpgrade) {
			l.reason = "helper_upgrade_required"
		}
		return err
	}
	l.reason = ""
	return nil
}
