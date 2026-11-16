package agentaccounts

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type sessionLimitsProvider struct {
	*fakeClaude
	homes []string
	read  func(context.Context, string) (domain.AgentAccountLimits, error)
}

func (p *sessionLimitsProvider) Identity(ctx context.Context, home string) (domain.AgentAccountIdentityCheck, error) {
	p.homes = append(p.homes, home)
	return p.fakeClaude.Identity(ctx, home)
}

func (p *sessionLimitsProvider) Limits(ctx context.Context, home string) (domain.AgentAccountLimits, error) {
	return p.read(ctx, home)
}

func sessionLimitsFixture(t *testing.T) (*Service, *memStore, *sessionLimitsProvider) {
	t.Helper()
	store := &memStore{
		accounts: []domain.AgentAccount{
			{ID: "a", Provider: domain.AgentAccountProviderClaude, Home: "/account-a", Email: "a@example.com"},
			{ID: "b", Provider: domain.AgentAccountProviderClaude, Home: "/account-b", Email: "b@example.com"},
		},
		defaults: map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "a"},
		bindings: map[domain.SessionID]string{"session": "b"},
	}
	provider := &sessionLimitsProvider{fakeClaude: &fakeClaude{
		signedInAs: map[string]string{"/account-a": "a@example.com", "/account-b": "b@example.com"},
		failing:    map[string]bool{},
	}}
	provider.read = func(context.Context, string) (domain.AgentAccountLimits, error) {
		return domain.AgentAccountLimits{FetchedAt: time.Unix(1, 0)}, nil
	}
	return New(store, []ports.AgentAccountProviderCLI{provider}, t.TempDir(), nil, nil), store, provider
}

func TestSessionLimitsRefreshesOnlyPinnedAccount(t *testing.T) {
	svc, store, provider := sessionLimitsFixture(t)
	ctx := context.Background()
	var reads int64
	provider.read = func(context.Context, string) (domain.AgentAccountLimits, error) {
		reads++
		return domain.AgentAccountLimits{FetchedAt: time.Unix(reads, 0)}, nil
	}
	for i, refresh := range []bool{false, false, true} {
		accountID, limits, err := svc.SessionLimits(ctx, "session", domain.HarnessClaudeCode, refresh)
		want := []int64{1, 1, 2}[i]
		if err != nil || accountID != "b" || limits.FetchedAt.Unix() != want {
			t.Fatalf("limits = %q, %+v, %v", accountID, limits, err)
		}
		// A changed default must not redirect an existing binding or its reads.
		store.defaults[domain.AgentAccountProviderClaude] = "device-claude"
	}
	if !reflect.DeepEqual(provider.homes, []string{"/account-b", "/account-b"}) {
		t.Fatalf("queried homes = %v", provider.homes)
	}
}

func TestSessionLimitsNeverUsesRetainedDisplayLimitsAfterReadFailure(t *testing.T) {
	for _, failure := range []string{"identity", "limits", "cancelled"} {
		t.Run(failure, func(t *testing.T) {
			svc, _, provider := sessionLimitsFixture(t)
			if _, _, err := svc.SessionLimits(context.Background(), "session", domain.HarnessClaudeCode, true); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch failure {
			case "identity":
				provider.failing["/account-b"] = true
			case "limits":
				provider.read = func(context.Context, string) (domain.AgentAccountLimits, error) {
					return domain.AgentAccountLimits{}, errors.New("temporary provider failure")
				}
			case "cancelled":
				provider.read = func(context.Context, string) (domain.AgentAccountLimits, error) {
					cancel()
					return domain.AgentAccountLimits{FetchedAt: time.Unix(2, 0)}, nil
				}
			}
			_, limits, err := svc.SessionLimits(ctx, "session", domain.HarnessClaudeCode, true)
			if err == nil || !limits.FetchedAt.IsZero() {
				t.Fatalf("failed fresh read returned stale limits: %+v, %v", limits, err)
			}
			if failure == "cancelled" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled read = %v", err)
			}
		})
	}
}

func TestSessionLimitsRefusesChangedBindingWithoutDefaultFallback(t *testing.T) {
	for _, change := range []string{"unbound", "removed", "removing", "provider", "during read", "removed during read", "identity"} {
		t.Run(change, func(t *testing.T) {
			svc, store, provider := sessionLimitsFixture(t)
			switch change {
			case "unbound":
				delete(store.bindings, "session")
			case "removed":
				store.accounts = store.accounts[:1]
			case "removing":
				svc.removing["b"] = true
			case "provider":
				store.accounts[1].Provider = domain.AgentAccountProviderCodex
			case "during read":
				provider.onIdentity = func(string) { store.bindings["session"] = "a" }
			case "removed during read":
				provider.onIdentity = func(string) { store.accounts = store.accounts[:1] }
			case "identity":
				provider.signedInAs["/account-b"] = "another@example.com"
			}
			_, limits, err := svc.SessionLimits(context.Background(), "session", domain.HarnessClaudeCode, true)
			if !errors.Is(err, ports.ErrAgentAccountChanged) || !limits.FetchedAt.IsZero() {
				t.Fatalf("changed account limits = %+v, %v", limits, err)
			}
			if len(provider.homes) > 1 || (len(provider.homes) == 1 && provider.homes[0] != "/account-b") {
				t.Fatalf("read another account: %v", provider.homes)
			}
		})
	}
}
