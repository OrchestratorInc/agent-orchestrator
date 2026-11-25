package devices

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Leases outlive the daemon. An app update or crash restarts it while agents
// keep working: without the record a session would lose its device mid-task,
// and its bound agent-device command would keep driving a device the daemon
// no longer knew was taken, free to hand to the next session.

type storedDevice struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Version  string `json:"version,omitempty"`
}

type storedLease struct {
	SessionID string       `json:"sessionId"`
	Provider  string       `json:"provider"`
	Device    storedDevice `json:"device"`
	Reason    string       `json:"reason,omitempty"`
	Command   string       `json:"agentDeviceCommand,omitempty"`
	Args      []string     `json:"agentDeviceArgs,omitempty"`
	OpenedAt  time.Time    `json:"openedAt"`
}

type leaseFile struct {
	Leases []storedLease `json:"leases"`
	// Owned lists the devices this service booted, which a later session may
	// reuse (see Service.owned).
	Owned []string `json:"owned,omitempty"`
}

func leasesPath(dataDir string) string { return filepath.Join(dataDir, "devices", "leases.json") }

// loadLeases reads the record a previous daemon left. A missing or unreadable
// file is an empty one: the worst case is the old behaviour.
func loadLeases(dataDir string) (map[domain.SessionID]Lease, map[string]bool) {
	leases, owned := map[domain.SessionID]Lease{}, map[string]bool{}
	raw, err := os.ReadFile(leasesPath(dataDir))
	if err != nil {
		return leases, owned
	}
	var file leaseFile
	if json.Unmarshal(raw, &file) != nil {
		return leases, owned
	}
	for _, l := range file.Leases {
		if l.SessionID == "" || l.Device.ID == "" {
			continue
		}
		leases[domain.SessionID(l.SessionID)] = Lease{
			SessionID: domain.SessionID(l.SessionID),
			Provider:  l.Provider,
			Device: ports.DeviceInfo{
				ID:       l.Device.ID,
				Name:     l.Device.Name,
				Platform: ports.DevicePlatform(l.Device.Platform),
				Version:  l.Device.Version,
			},
			Reason:       l.Reason,
			AgentControl: ports.DeviceAgentControl{Command: l.Command, Args: l.Args},
			OpenedAt:     l.OpenedAt,
		}
	}
	for _, id := range file.Owned {
		owned[id] = true
	}
	return leases, owned
}

// saveLocked writes the current leases. Callers hold s.mu. A failed write is
// logged: the leases still hold for this daemon's life.
func (s *Service) saveLocked() {
	file := leaseFile{Leases: make([]storedLease, 0, len(s.leases))}
	for _, l := range s.sortedLeasesLocked() {
		file.Leases = append(file.Leases, storedLease{
			SessionID: string(l.SessionID),
			Provider:  l.Provider,
			Device: storedDevice{
				ID:       l.Device.ID,
				Name:     l.Device.Name,
				Platform: string(l.Device.Platform),
				Version:  l.Device.Version,
			},
			Reason:   l.Reason,
			Command:  l.AgentControl.Command,
			Args:     l.AgentControl.Args,
			OpenedAt: l.OpenedAt,
		})
	}
	for id := range s.owned {
		file.Owned = append(file.Owned, id)
	}
	if err := writeFileAtomic(leasesPath(s.cfg.DataDir), file); err != nil {
		s.log.Warn("save device leases", "err", err)
	}
}

func writeFileAtomic(path string, v any) error {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		return errors.Join(err, os.Remove(tmp))
	}
	return nil
}
