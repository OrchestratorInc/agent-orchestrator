package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"sync/atomic"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/modelcatalog"
	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// A login detected by any readiness check — AO's login terminal, the agent's
// own CLI, or a renewed token — must reach caches built while signed out.
func TestReadinessReportsRecoveredAuthenticationOnce(t *testing.T) {
	t.Parallel()
	var status atomic.Value
	status.Store(ports.AgentAuthStatusUnauthorized)
	agent := &readinessTestAgent{
		resolve: func(context.Context) (string, error) { return "/bin/claude", nil },
		auth: func(context.Context) (ports.AgentAuthStatus, error) {
			return status.Load().(ports.AgentAuthStatus), nil
		},
	}
	recovered := make(chan string, 4)
	coordinator := newReadinessCoordinator(readinessCoordinatorConfig{
		Agents:                    []agentregistry.HarnessAgent{readinessHarness("claude-code", "Claude Code", agent)},
		OnAuthenticationRecovered: func(agentID string) { recovered <- agentID },
	})
	ensure := func() {
		t.Helper()
		if _, err := coordinator.Force(context.Background(), []string{"claude-code"}, domain.AgentReadinessPurposeDisplay); err != nil {
			t.Fatal(err)
		}
	}

	ensure()
	select {
	case id := <-recovered:
		t.Fatalf("first observation (unknown to unauthorized) reported recovery for %s", id)
	case <-time.After(50 * time.Millisecond):
	}

	status.Store(ports.AgentAuthStatusAuthorized)
	ensure()
	select {
	case id := <-recovered:
		if id != "claude-code" {
			t.Fatalf("recovered agent = %q", id)
		}
	case <-time.After(time.Second):
		t.Fatal("signed-out to signed-in transition was not reported")
	}

	ensure()
	select {
	case id := <-recovered:
		t.Fatalf("a repeated authorized observation reported recovery again for %s", id)
	case <-time.After(50 * time.Millisecond):
	}
}

func TestModelCatalogClassifiesAuthFailuresForClients(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want string
	}{
		{"rejected credential", fmt.Errorf("claude-code model discovery: %w", wrapAuthError(ports.ErrAgentModelDiscoveryCredentialRejected, "Anthropic rejected the credential")), ports.ModelCatalogWarningAuthRequired},
		{"expired stored login", fmt.Errorf("claude-code model discovery: %w", wrapAuthError(ports.ErrAgentModelDiscoveryCredentialExpired, "login token expired")), ports.ModelCatalogWarningAuthExpired},
		{"ordinary failure", errors.New("provider offline"), ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &fakeModelCache{}
			discoverer := &fakeModelDiscoverer{version: "v1", err: tc.err, catalog: ports.AgentModelCatalog{
				SelectionMode: ports.ModelSelectionCatalog,
				Models:        []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}},
				Source:        "catalog",
			}}
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)
			catalog, err := svc.Models(context.Background(), "claude-code", "", true)
			if err != nil {
				t.Fatal(err)
			}
			if catalog.WarningCode != tc.want {
				t.Fatalf("warningCode = %q, want %q (warning %q)", catalog.WarningCode, tc.want, catalog.Warning)
			}
			if catalog.Warning == "" {
				t.Fatal("the provider's own explanation must stay available as the warning detail")
			}
		})
	}
}

func TestSignInRequiredCatalogCarriesAuthCode(t *testing.T) {
	discoverer := &fakeModelDiscoverer{version: "v1", err: fmt.Errorf("kiro model discovery: %w", ports.ErrAgentModelDiscoverySignInRequired)}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("kiro", "Kiro", nil)}, &fakeModelCache{}, discoverer)
	catalog, err := svc.Models(context.Background(), "kiro", "", true)
	if err != nil {
		t.Fatal(err)
	}
	if catalog.WarningCode != ports.ModelCatalogWarningAuthRequired {
		t.Fatalf("warningCode = %q, want auth_required", catalog.WarningCode)
	}
}

func TestSignedOutSavedAccountKeepsCatalogUntilSignIn(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cached    bool
		firstRead bool
	}{
		{name: "queued catalog", cached: true},
		{name: "uncached revalidation"},
		{name: "uncached picker read", firstRead: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			cache := &fakeModelCache{}
			svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, successfulModelDiscoverer())
			wantModels := []ports.AgentModelInfo{}
			if tc.cached {
				initial, err := svc.RevalidateModels(ctx, "claude-code", "")
				if err != nil || len(initial.Models) != 1 {
					t.Fatalf("initial catalog = %#v, error = %v", initial, err)
				}
				wantModels = initial.Models
				svc.requeueCatalog("", initial)
			}
			svc.SetSavedDefaultLaunchEnv(func(context.Context, domain.AgentHarness) (map[string]string, error) {
				return nil, fmt.Errorf("saved account: %w", ports.ErrAgentAccountDefaultUnusable)
			})

			var catalog ports.AgentModelCatalog
			var err error
			if tc.firstRead {
				catalog, err = svc.Models(ctx, "claude-code", "", false)
			} else {
				catalog, err = svc.RevalidateModels(ctx, "claude-code", "")
			}
			if err != nil {
				t.Fatalf("signed-out account returned an error instead of a catalog: %v", err)
			}
			if !reflect.DeepEqual(catalog.Models, wantModels) || catalog.WarningCode != ports.ModelCatalogWarningAuthRequired || catalog.Warning == "" {
				t.Fatalf("catalog = %#v, want the saved models with a sign-in warning", catalog)
			}
			if catalog.RefreshState != "idle" || catalog.RefreshError != "" || catalog.RetryAt != nil || catalog.RefreshRecommended || catalog.Stale {
				t.Fatalf("signed-out catalog is still loading or retrying: %#v", catalog)
			}
			if !tc.cached && (!catalog.AllowCustom || catalog.SelectionMode != ports.ModelSelectionText) {
				t.Fatalf("uncached catalog lost manual model entry: %#v", catalog)
			}
			assertIdleWithoutRetries(t, cache, "claude-code")
			read, err := svc.Models(ctx, "claude-code", "", false)
			if err != nil || !reflect.DeepEqual(read.Models, wantModels) || read.WarningCode != ports.ModelCatalogWarningAuthRequired || read.RefreshState != "idle" {
				t.Fatalf("cache-first read = %#v, error = %v; want the persisted sign-in state", read, err)
			}
		})
	}
}

func TestSavedAccountLookupErrorStillFailsModelRevalidation(t *testing.T) {
	want := errors.New("account store unavailable")
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, &fakeModelCache{}, successfulModelDiscoverer())
	svc.SetSavedDefaultLaunchEnv(func(context.Context, domain.AgentHarness) (map[string]string, error) {
		return nil, want
	})
	if _, err := svc.RevalidateModels(context.Background(), "claude-code", ""); !errors.Is(err, want) {
		t.Fatalf("account lookup error = %v, want %v", err, want)
	}
}

// After a login, the cached rejection must disappear immediately instead of
// lingering until the background rediscovery finishes.
func TestInvalidateModelCatalogsClearsAuthWarningAndRediscovers(t *testing.T) {
	cache := &fakeModelCache{}
	discoverer := &fakeModelDiscoverer{version: "v1", err: wrapAuthError(ports.ErrAgentModelDiscoveryCredentialRejected, "Anthropic rejected the credential"), catalog: ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "opus", Label: "Opus"}},
	}}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)
	if catalog, err := svc.Models(context.Background(), "claude-code", "", true); err != nil || catalog.WarningCode == "" {
		t.Fatalf("setup catalog = %+v, err = %v", catalog, err)
	}

	discoverer.mu.Lock()
	discoverer.err = nil
	discoverer.catalog = ports.AgentModelCatalog{
		SelectionMode: ports.ModelSelectionCatalog,
		Models:        []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}},
		Source:        "provider",
	}
	discoverer.mu.Unlock()
	svc.InvalidateModelCatalogs("claude-code")

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		record, ok, _ := cache.GetAgentModelCatalog(context.Background(), "claude-code", "")
		var catalog ports.AgentModelCatalog
		if ok && json.Unmarshal([]byte(record.CatalogJSON), &catalog) == nil &&
			catalog.Warning == "" && catalog.WarningCode == "" && len(catalog.Models) == 1 && catalog.Models[0].ID == "claude-opus-5-5" {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("invalidation did not clear the auth warning and rediscover the catalog")
}

// Exercise the real discoverer and shared cache together: a failure supplies
// no replacement list, so the last provider catalog must survive and persist.
func TestClaudeDiscoveryFailureUsesCachedModelsWithoutStaticFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	t.Setenv("ANTHROPIC_MODEL", "")
	var fail atomic.Bool
	discoverer := modelcatalog.Discoverer{ClaudeModels: func(context.Context, ports.AgentModelDiscoveryRequest) ([]ports.AgentModelInfo, error) {
		if fail.Load() {
			return nil, wrapAuthError(ports.ErrAgentModelDiscoveryCredentialExpired, "login token expired")
		}
		return []ports.AgentModelInfo{{ID: "claude-opus-5-5", Label: "Claude Opus 5.5"}}, nil
	}}
	cache := &fakeModelCache{}
	svc := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, cache, discoverer)
	fresh, err := svc.Models(context.Background(), "claude-code", "", true)
	if err != nil || len(fresh.Models) != 1 || fresh.Source != "provider" {
		t.Fatalf("initial discovery = %#v, %v", fresh, err)
	}
	fail.Store(true)
	for _, refresh := range []bool{true, false} {
		got, err := svc.Models(context.Background(), "claude-code", "", refresh)
		if err != nil || !reflect.DeepEqual(got.Models, fresh.Models) || !got.Stale || got.WarningCode != ports.ModelCatalogWarningAuthExpired {
			t.Fatalf("refresh=%v: catalog = %#v, %v; want cached provider models and auth warning", refresh, got, err)
		}
	}
	// A scope with no cache remains empty and permits manual entry.
	emptyService := newService([]agentregistry.HarnessAgent{harnessAgent("claude-code", "Claude Code", nil)}, &fakeModelCache{}, discoverer)
	got, err := emptyService.Models(context.Background(), "claude-code", "", true)
	if err != nil || len(got.Models) != 0 || !got.AllowCustom || got.WarningCode != ports.ModelCatalogWarningAuthExpired {
		t.Fatalf("uncached catalog = %#v, %v; want manual entry with the auth warning", got, err)
	}
}

type testAuthError struct {
	kind    error
	message string
}

func (e testAuthError) Error() string { return e.message }
func (e testAuthError) Unwrap() error { return e.kind }

func wrapAuthError(kind error, message string) error {
	return testAuthError{kind: kind, message: message}
}
