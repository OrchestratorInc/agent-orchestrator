package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/testingdesktop/cua"
	localtarget "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testingtarget/local"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	testingsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/testing"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

func TestTestingWiringMissingProvidersFailsExplicitly(t *testing.T) {
	store := sqlitetest.MustOpen(t)
	svc := newTestingService(config.Config{DataDir: t.TempDir()}, store, testingProviders{})
	defer svc.Close()
	_, err := svc.CreateRun(context.Background(), testingsvc.CreateRunInput{})
	var apiError *apierr.Error
	if !errors.As(err, &apiError) || apiError.Code != "TESTING_PROVIDER_NOT_CONFIGURED" {
		t.Fatal("missing providers did not fail explicitly", err)
	}
}

// Embedded interfaces deliberately panic on unexpected lifecycle/input calls:
// composing providers and creating a run must not start any desktop or worker.
type wiringDesktop struct {
	ports.TestingDesktopControl
	mode   cua.DeliveryMode
	closed bool
}

func (d *wiringDesktop) DeliveryMode() cua.DeliveryMode { return d.mode }
func (*wiringDesktop) StartRecording(context.Context, domain.TestTargetIdentity, string) (cua.RecordingResult, error) {
	return cua.RecordingResult{Gap: "window recording gap"}, nil
}
func (*wiringDesktop) StopRecording(context.Context, domain.TestTargetIdentity) (cua.RecordingResult, error) {
	return cua.RecordingResult{Gap: "window recording gap"}, nil
}
func (*wiringDesktop) Release(context.Context, domain.TestTargetIdentity) error { return nil }
func (d *wiringDesktop) Close(context.Context) error                            { d.closed = true; return nil }

type wiringTarget struct{ ports.TestingTargetEnvironment }
type wiringWorker struct{ testingsvc.WorkerLauncher }

func TestTestingProviderComposition(t *testing.T) {
	for _, tc := range []struct {
		name, checkout, mode string
		wantMode             cua.DeliveryMode
		configured           bool
	}{
		{name: "unset recipe", wantMode: cua.Background},
		{name: "configured background", checkout: "/isolated/checkout", wantMode: cua.Background, configured: true},
		{name: "explicit foreground", checkout: "/isolated/checkout", mode: "foreground", wantMode: cua.Foreground, configured: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := config.Config{DataDir: t.TempDir()}
			env := map[string]string{"AO_TESTING_TARGET_CHECKOUT": tc.checkout, "AO_TESTING_DESKTOP_DELIVERY": tc.mode}
			desktop := &wiringDesktop{}
			created := false
			providers, err := testingProvidersFromEnv(cfg, func(key string) string { return env[key] }, &wiringTarget{}, func(got cua.Config) (testingDesktopAdapter, error) {
				created = true
				if got.DataDir != cfg.DataDir || got.DeliveryMode != tc.wantMode || got.Runner != nil || got.ProcessStartedAt != nil {
					t.Fatalf("wrong desktop composition: %+v", got)
				}
				desktop.mode = got.DeliveryMode
				return desktop, nil
			})
			if err != nil {
				t.Fatal(err)
			}
			if tc.configured {
				policy := providers.Desktop.(ports.TestingDesktopPolicy)
				if policy.DeliveryMode() != string(tc.wantMode) || policy.InputDeliveryMode("click") != string(tc.wantMode) || policy.InputDeliveryMode("type") != string(tc.wantMode) || policy.InputDeliveryMode("key") != string(tc.wantMode) {
					t.Fatal("desktop policy bridge lost mode")
				}
				gap, err := providers.Desktop.(ports.TestingDesktopRecorder).StartRecording(context.Background(), domain.TestTargetIdentity{}, "")
				if err != nil || gap.Gap != "window recording gap" {
					t.Fatal("recorder bridge lost gap", err)
				}
			} else if created || providers.Close != nil || providers.Desktop != nil {
				t.Fatal("unset recipe constructed a desktop provider")
			}
			store := sqlitetest.MustOpen(t)
			if err := store.UpsertProject(context.Background(), domain.ProjectRecord{ID: "testing", Path: cfg.DataDir, RegisteredAt: time.Now()}); err != nil {
				t.Fatal(err)
			}
			providers.Workers = &wiringWorker{}
			svc := newTestingService(cfg, store, providers)
			defer svc.Close()
			run, err := svc.CreateRun(context.Background(), testingsvc.CreateRunInput{ProjectID: "testing", IssueSnapshot: "quoted issue", CommitSHA: "abc", RecipeID: "local-ao", Requester: "maintainer"})
			if !tc.configured {
				var apiError *apierr.Error
				if providers.Target != nil || len(providers.Recipes) != 0 || !errors.As(err, &apiError) || apiError.Code != "TESTING_PROVIDER_NOT_CONFIGURED" {
					t.Fatal("unset recipe was configured", err)
				}
			} else {
				var recipe testingsvc.Recipe
				if err != nil || json.Unmarshal([]byte(run.RecipeSnapshot), &recipe) != nil || recipe.CheckoutPath != tc.checkout || recipe.DeliveryMode != string(tc.wantMode) || !recipe.VisualMarker {
					t.Fatal("configured recipe not retained", run, err)
				}
			}
			if tc.configured {
				if err := svc.Close(); err != nil || !desktop.closed {
					t.Fatal("service shutdown did not close the desktop provider", err)
				}
			}
		})
	}
}

func TestTestingProviderCompositionRejectsInvalidModeAndFactoryError(t *testing.T) {
	_, err := testingProvidersFromEnv(config.Config{}, func(string) string { return "auto" }, nil, func(cua.Config) (testingDesktopAdapter, error) {
		t.Fatal("invalid mode reached desktop factory")
		return nil, nil
	})
	if err == nil {
		t.Fatal("invalid mode accepted")
	}
	_, err = testingProvidersFromEnv(config.Config{}, func(key string) string {
		if key == "AO_TESTING_TARGET_CHECKOUT" {
			return "/isolated/checkout"
		}
		return ""
	}, nil, func(cua.Config) (testingDesktopAdapter, error) {
		return nil, errors.New("provider configuration failed")
	})
	if err == nil {
		t.Fatal("provider construction failure hidden")
	}
}

func TestTestingProductionCompositionConstructsAdaptersWithoutLaunching(t *testing.T) {
	t.Setenv("AO_TESTING_TARGET_CHECKOUT", "/prepared/isolated-checkout")
	t.Setenv("AO_TESTING_DESKTOP_DELIVERY", "foreground")
	// Construction neither creates this directory nor launches native processes.
	providers, err := configuredTestingProviders(config.Config{DataDir: "/tmp/ao-wiring-construction"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := providers.Target.(*localtarget.Adapter); !ok || !providers.Recipes["local-ao"].VisualMarker {
		t.Fatal("production composition did not select the isolated local target")
	}
	if err := providers.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestTestingRecordingBridgePreservesFullMetadata(t *testing.T) {
	r := cua.RecordingResult{Path: "/supervisor/window.mov", MIMEType: "video/quicktime", Width: 2, Height: 2, Duration: time.Second,
		StartedAt: time.Now(), StoppedAt: time.Now(), RecorderPID: 42, Gap: "declared gap", StagingPath: "/native/owned.mov", StagingCleanup: "verified absent"}
	want, err := json.Marshal(r)
	got, gotErr := json.Marshal(recordingResult(r))
	if err != nil || gotErr != nil || !reflect.DeepEqual(got, want) {
		t.Fatal("provider recording metadata changed during translation", string(got), err, gotErr)
	}
}

func TestTestingInputRefusalBridge(t *testing.T) {
	if !errors.Is(testingInputError(errors.Join(cua.ErrRefused, errors.New("stale frame"))), ports.ErrTestingInputRefused) {
		t.Fatal("Cua refusal was not mapped to the provider-neutral refusal")
	}
	if errors.Is(testingInputError(cua.ErrProvider), ports.ErrTestingInputRefused) {
		t.Fatal("uncertain delivery was reported as no input")
	}
}
