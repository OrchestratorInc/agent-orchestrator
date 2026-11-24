package local

import (
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// Stream qualities are kept on disk as well: a daemon that restarts (an app
// update, a crash) starts a fresh hub, and the phone that asked for a quality
// only asks again when its connection changes.

type storedTuning struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Platform string `json:"platform"`
	Quality  string `json:"quality"`
}

func (p *Provider) tunedPath() string { return filepath.Join(p.stateRoot(), "stream-quality.json") }

func (p *Provider) loadTuned() map[string]tunedStream {
	out := map[string]tunedStream{}
	raw, err := os.ReadFile(p.tunedPath())
	if err != nil {
		return out
	}
	var stored []storedTuning
	if json.Unmarshal(raw, &stored) != nil {
		return out
	}
	for _, s := range stored {
		if s.ID == "" {
			continue
		}
		out[s.ID] = tunedStream{
			device:  ports.DeviceInfo{ID: s.ID, Name: s.Name, Platform: ports.DevicePlatform(s.Platform)},
			quality: ports.DeviceStreamQuality(s.Quality),
		}
	}
	return out
}

// saveTunedLocked writes p.tuned. Callers hold p.mu.
func (p *Provider) saveTunedLocked() {
	stored := make([]storedTuning, 0, len(p.tuned))
	for _, t := range p.tuned {
		stored = append(stored, storedTuning{ID: t.device.ID, Name: t.device.Name, Platform: string(t.device.Platform), Quality: string(t.quality)})
	}
	raw, err := json.Marshal(stored)
	if err != nil {
		return
	}
	if err := os.MkdirAll(p.stateRoot(), 0o750); err != nil {
		return
	}
	if err := os.WriteFile(p.tunedPath(), raw, 0o600); err != nil {
		p.log.Debug("save device stream qualities", "err", err)
	}
}
