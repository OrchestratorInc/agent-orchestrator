package agent

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	agentregistry "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/registry"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type managedReadinessTestSource func(context.Context, domain.AgentHarness, domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool)

func (f managedReadinessTestSource) AuthenticationReadiness(ctx context.Context, harness domain.AgentHarness, purpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
	return f(ctx, harness, purpose)
}

func TestManagedProviderReadinessReplacesNativeAuthCheck(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		for _, purpose := range []domain.AgentReadinessPurpose{domain.AgentReadinessPurposeDisplay, domain.AgentReadinessPurposeLaunch} {
			for _, authorized := range []bool{false, true} {
				name := string(harness) + "/" + string(purpose)
				if authorized {
					name += "/signed-in"
				} else {
					name += "/signed-out"
				}
				t.Run(name, func(t *testing.T) {
					native := &readinessTestAgent{
						resolve: func(context.Context) (string, error) { return "/fake/agent", nil },
						auth: func(context.Context) (ports.AgentAuthStatus, error) {
							// Native state deliberately contradicts managed state.
							if authorized {
								return ports.AgentAuthStatusUnauthorized, nil
							}
							return ports.AgentAuthStatusAuthorized, nil
						},
					}
					svc := NewWithAgents([]agentregistry.HarnessAgent{readinessHarness(string(harness), string(harness), native)})
					var checks atomic.Int32
					want := domain.AgentAuthenticationUnauthorized
					if authorized {
						want = domain.AgentAuthenticationAuthorized
					}
					svc.SetManagedProviderReadiness(managedReadinessTestSource(func(ctx context.Context, got domain.AgentHarness, gotPurpose domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
						checks.Add(1)
						if got != harness || gotPurpose != purpose {
							t.Errorf("managed check = %s/%s, want %s/%s", got, gotPurpose, harness, purpose)
						}
						if _, ok := ctx.Deadline(); !ok {
							t.Error("managed check must have a bounded context")
						}
						return successfulAuthentication(time.Now(), want, domain.AgentReadinessReasonAuthorized, "managed"), true
					}))
					got, err := svc.EnsureAgentReadiness(context.Background(), string(harness), purpose)
					if err != nil {
						t.Fatal(err)
					}
					if got.Authentication.State != want || (got.EffectiveReadiness == domain.AgentReadinessReady) != authorized {
						t.Fatalf("readiness = %+v, want auth=%s", got, want)
					}
					if native.authCalls.Load() != 0 || checks.Load() != 1 {
						t.Fatalf("checks: native=%d managed=%d", native.authCalls.Load(), checks.Load())
					}
				})
			}
		}
	}
}

func TestManagedReadinessPreservesNativeProviders(t *testing.T) {
	for _, authorized := range []bool{true, false} {
		native := &readinessTestAgent{
			resolve: func(context.Context) (string, error) { return "/fake/cursor", nil },
			auth: func(context.Context) (ports.AgentAuthStatus, error) {
				if authorized {
					return ports.AgentAuthStatusAuthorized, nil
				}
				return ports.AgentAuthStatusUnauthorized, nil
			},
		}
		svc := NewWithAgents([]agentregistry.HarnessAgent{readinessHarness("cursor", "Cursor", native)})
		svc.SetManagedProviderReadiness(managedReadinessTestSource(func(context.Context, domain.AgentHarness, domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
			t.Error("unmanaged provider must not consult Account Manager")
			return domain.AgentAuthenticationObservation{}, true
		}))
		got, err := svc.EnsureAgentReadiness(context.Background(), "cursor", domain.AgentReadinessPurposeLaunch)
		if err != nil {
			t.Fatal(err)
		}
		if (got.Authentication.State == domain.AgentAuthenticationAuthorized) != authorized || native.authCalls.Load() != 1 {
			t.Fatalf("native readiness changed: %+v, checks=%d", got, native.authCalls.Load())
		}
	}
}

func TestManagedReadinessInvalidatesPreviouslyCachedNativeAuthentication(t *testing.T) {
	for _, harness := range []domain.AgentHarness{domain.HarnessCodex, domain.HarnessClaudeCode} {
		t.Run(string(harness), func(t *testing.T) {
			native := &readinessTestAgent{
				resolve: func(context.Context) (string, error) { return "/fake/agent", nil },
				auth:    func(context.Context) (ports.AgentAuthStatus, error) { return ports.AgentAuthStatusAuthorized, nil },
			}
			svc := NewWithAgents([]agentregistry.HarnessAgent{readinessHarness(string(harness), string(harness), native)})
			before, err := svc.EnsureAgentReadiness(context.Background(), string(harness), domain.AgentReadinessPurposeDisplay)
			if err != nil || before.Authentication.State != domain.AgentAuthenticationAuthorized {
				t.Fatalf("native initial state=%+v error=%v", before, err)
			}
			svc.SetManagedProviderReadiness(managedReadinessTestSource(func(context.Context, domain.AgentHarness, domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
				return successfulAuthentication(time.Now(), domain.AgentAuthenticationUnauthorized, domain.AgentReadinessReasonUnauthorized, "managed sign-in required"), true
			}))
			after, err := svc.EnsureAgentReadiness(context.Background(), string(harness), domain.AgentReadinessPurposeDisplay)
			if err != nil || after.Authentication.State != domain.AgentAuthenticationUnauthorized {
				t.Fatalf("cached native status survived managed wiring: %+v error=%v", after, err)
			}
			if native.authCalls.Load() != 1 || native.resolveCalls.Load() != 1 {
				t.Fatalf("installation/native unnecessarily rechecked: auth=%d resolve=%d", native.authCalls.Load(), native.resolveCalls.Load())
			}
		})
	}
}

func TestManagedAccountDoesNotBypassMissingInstallation(t *testing.T) {
	native := &readinessTestAgent{resolve: func(context.Context) (string, error) {
		return "", errors.New("binary missing")
	}}
	svc := NewWithAgents([]agentregistry.HarnessAgent{readinessHarness("claude-code", "Claude Code", native)})
	svc.SetManagedProviderReadiness(managedReadinessTestSource(func(context.Context, domain.AgentHarness, domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
		t.Error("authentication should be skipped when installation fails")
		return successfulAuthentication(time.Now(), domain.AgentAuthenticationAuthorized, domain.AgentReadinessReasonAuthorized, "managed"), true
	}))
	got, err := svc.EnsureAgentReadiness(context.Background(), "claude-code", domain.AgentReadinessPurposeLaunch)
	if err != nil {
		t.Fatal(err)
	}
	if got.EffectiveReadiness == domain.AgentReadinessReady {
		t.Fatalf("missing binary incorrectly became ready: %+v", got)
	}
}
