package primeagent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/hookutil"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Prime Agent submits the prompt from its command line only after its first-run
// onboarding screen is answered, and existing credentials don't skip that
// screen. AO gives Prime an isolated profile that starts without the marker, so
// an unattended worker would wait on the screen forever. Prime's own marker
// skips the flow without choosing anything for the user: trace sharing stays off
// because it is opt-in.
const (
	settingsFileName   = "settings.json"
	onboardingShownKey = "onboardingShown"
)

var _ interface {
	PreLaunch(context.Context, ports.LaunchConfig) error
} = (*Plugin)(nil)

// PreLaunch records first-run onboarding as done in AO's Prime profile.
func (p *Plugin) PreLaunch(ctx context.Context, cfg ports.LaunchConfig) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	dir, err := primeDataDir(cfg.DataDir)
	if err != nil {
		return err
	}
	if err := markOnboardingShown(filepath.Join(dir, settingsFileName)); err != nil {
		return fmt.Errorf("prime-agent.PreLaunch: %w", err)
	}
	return ctx.Err()
}

// markOnboardingShown sets the marker and keeps every other setting. A file AO
// cannot round-trip is left alone and reported: launching anyway would strand
// the worker on the onboarding screen with no error.
func markOnboardingShown(path string) error {
	settings := map[string]json.RawMessage{}
	data, err := os.ReadFile(path) //nolint:gosec // stable AO-owned path under DataDir
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read settings: %w", err)
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &settings); err != nil {
			return fmt.Errorf("parse settings %s: %w", path, err)
		}
		// A top-level null decodes without error but leaves the map nil.
		if settings == nil {
			return fmt.Errorf("parse settings %s: not a JSON object", path)
		}
	}
	var shown bool
	if raw, ok := settings[onboardingShownKey]; ok && json.Unmarshal(raw, &shown) == nil && shown {
		return nil
	}
	settings[onboardingShownKey] = json.RawMessage("true")
	encoded, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("encode settings: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}
	if err := hookutil.AtomicWriteFile(path, append(encoded, '\n'), 0o600); err != nil {
		return fmt.Errorf("write settings: %w", err)
	}
	return nil
}
