package zcodeappserver_test

import (
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/chatdriver/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// Phase 2 ships the driver in registry.Build: a registered driver whose Probe
// succeeds is what makes `--mode chat` and the default chat spawn use the
// app-server instead of falling back to the one-shot TUI. The flip from the
// Phase 0 "not shipped yet" assertion is deliberate, now that every advertised
// capability is backed by a Phase 1 capture.
func TestZcodeIsShipped(t *testing.T) {
	r := registry.Build(nil, nil)
	if !r.SupportsChat(domain.HarnessZcode) {
		t.Fatal("zcode has no shipped chat driver")
	}
	driver, err := r.Driver(domain.HarnessZcode)
	if err != nil {
		t.Fatalf("Driver(zcode) = %v", err)
	}
	if driver.Harness() != domain.HarnessZcode {
		t.Fatalf("Harness = %q", driver.Harness())
	}
}
