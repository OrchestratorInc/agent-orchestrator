package daemon

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/testingdesktop/cua"
	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/testingevidence"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	testingsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testing"
	sessionmanager "github.com/aoagents/agent-orchestrator/backend/internal/session_manager"
)

// testingProviders is the single composition point for slices A, D and E.
// Empty providers deliberately keep creation/dispatch unavailable. Management
// cancellation and saved evidence use durable state independently of providers.
type testingProviders struct {
	Target  ports.TestingTargetEnvironment
	Desktop ports.TestingDesktopControl
	Workers testingsvc.WorkerLauncher
	Recipes map[string]testingsvc.Recipe
	Close   func(context.Context) error
}

// Provider-specific recording and policy types are translated here; the
// service only sees the provider-neutral desktop port and its extensions.
type testingDesktopAdapter interface {
	ports.TestingDesktopControl
	DeliveryMode() cua.DeliveryMode
	StartRecording(context.Context, domain.TestTargetIdentity, string) (cua.RecordingResult, error)
	StopRecording(context.Context, domain.TestTargetIdentity) (cua.RecordingResult, error)
	Release(context.Context, domain.TestTargetIdentity) error
	Close(context.Context) error
}

type testingDesktopBridge struct{ testingDesktopAdapter }

func (d testingDesktopBridge) DeliveryMode() string {
	return string(d.testingDesktopAdapter.DeliveryMode())
}
func (d testingDesktopBridge) InputDeliveryMode(tool string) string {
	if tool == "click" {
		return string(cua.Background)
	}
	return d.DeliveryMode()
}
func (d testingDesktopBridge) StartRecording(ctx context.Context, target domain.TestTargetIdentity, dir string) (ports.TestingRecordingResult, error) {
	result, err := d.testingDesktopAdapter.StartRecording(ctx, target, dir)
	return recordingResult(result), err
}
func (d testingDesktopBridge) StopRecording(ctx context.Context, target domain.TestTargetIdentity) (ports.TestingRecordingResult, error) {
	result, err := d.testingDesktopAdapter.StopRecording(ctx, target)
	return recordingResult(result), err
}
func recordingResult(result cua.RecordingResult) ports.TestingRecordingResult {
	// TODO(testing-recorder): copy recording metadata once D's recorder lands.
	return ports.TestingRecordingResult{Gap: result.Gap}
}

func configuredTestingProviders(cfg config.Config) (testingProviders, error) {
	// TODO(testing-target): construct E's local target adapter here when merged.
	return testingProvidersFromEnv(cfg, os.Getenv, nil, func(cfg cua.Config) (testingDesktopAdapter, error) { return cua.New(cfg) })
}

func testingProvidersFromEnv(cfg config.Config, getenv func(string) string, target ports.TestingTargetEnvironment, makeDesktop func(cua.Config) (testingDesktopAdapter, error)) (testingProviders, error) {
	mode := cua.DeliveryMode(getenv("AO_TESTING_DESKTOP_DELIVERY"))
	if mode == "" {
		mode = cua.Background
	}
	if mode != cua.Background && mode != cua.Foreground {
		return testingProviders{}, fmt.Errorf("AO_TESTING_DESKTOP_DELIVERY must be background or foreground")
	}
	checkout := getenv("AO_TESTING_TARGET_CHECKOUT")
	if checkout == "" {
		return testingProviders{}, nil
	}
	desktop, err := makeDesktop(cua.Config{DataDir: cfg.DataDir, DeliveryMode: mode})
	if err != nil {
		return testingProviders{}, fmt.Errorf("configure testing desktop: %w", err)
	}
	providers := testingProviders{Target: target, Desktop: testingDesktopBridge{desktop}, Close: desktop.Close,
		Recipes: map[string]testingsvc.Recipe{"local-ao": {ID: "local-ao", CheckoutPath: checkout, Snapshot: "isolated local AO checkout", DeliveryMode: string(mode)}}}
	return providers, nil
}

func newTestingService(cfg config.Config, store testingsvc.Store, providers testingProviders) *testingsvc.Service {
	home, _ := os.UserHomeDir()
	return testingsvc.New(testingsvc.Deps{Store: store, Target: providers.Target, Desktop: providers.Desktop, Workers: providers.Workers, Recipes: providers.Recipes, Evidence: testingevidence.New(cfg.DataDir, store), EvidenceRoot: filepath.Join(cfg.DataDir, "testing"), TargetStateRoot: filepath.Join(home, ".ao", "dev", "agentic-target")})
}

// wireTestingService binds both sides before startup recovery can restore workers.
func wireTestingService(cfg config.Config, store testingsvc.Store, manager *sessionmanager.Manager, providers testingProviders) *testingsvc.Service {
	providers.Workers = manager
	svc := newTestingService(cfg, store, providers)
	manager.SetTestingProfileResolver(svc)
	return svc
}
