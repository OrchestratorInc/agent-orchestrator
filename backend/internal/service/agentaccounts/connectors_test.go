package agentaccounts

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// fakeConnectorClaude is a Claude provider whose accounts carry connectors.
type fakeConnectorClaude struct {
	fakeClaude
	connectorsMu sync.Mutex
	homes        []string // the home of every read, in order
	connectors   []domain.AgentConnector
	connectorErr error
}

func (f *fakeConnectorClaude) Connectors(_ context.Context, home string) ([]domain.AgentConnector, error) {
	f.connectorsMu.Lock()
	defer f.connectorsMu.Unlock()
	f.homes = append(f.homes, home)
	return f.connectors, f.connectorErr
}

func (f *fakeConnectorClaude) reads() []string {
	f.connectorsMu.Lock()
	defer f.connectorsMu.Unlock()
	return append([]string(nil), f.homes...)
}

func connectorTestService(t *testing.T, provider ports.AgentAccountProviderCLI, accounts ...domain.AgentAccount) (*Service, *time.Time) {
	t.Helper()
	svc := New(&memStore{accounts: accounts}, []ports.AgentAccountProviderCLI{provider}, t.TempDir(), nil, nil)
	now := time.Date(2026, 11, 27, 10, 0, 0, 0, time.UTC)
	svc.now = func() time.Time { return now }
	return svc, &now
}

func TestConnectorsReadsAndReusesAReading(t *testing.T) {
	ctx := context.Background()
	provider := &fakeConnectorClaude{connectors: []domain.AgentConnector{
		{Name: "Gmail", Status: domain.AgentConnectorConnected},
		{Name: "AI Producer", Status: domain.AgentConnectorNeedsAuth},
	}}
	work := domain.AgentAccount{ID: "work", Provider: domain.AgentAccountProviderClaude, Home: "/accounts/work", Kind: domain.AgentAccountKindSubscription}
	svc, now := connectorTestService(t, provider, work)

	reading, err := svc.Connectors(ctx, "work", false)
	if err != nil {
		t.Fatal(err)
	}
	if reading.AccountID != "work" || reading.Unavailable != "" || len(reading.Connectors) != 2 || !reading.CheckedAt.Equal(*now) {
		t.Fatalf("reading = %+v", reading)
	}
	if got := provider.reads(); len(got) != 1 || got[0] != "/accounts/work" {
		t.Fatalf("reads = %v, want one of the account's own folder", got)
	}

	// Within the TTL the reading is reused; a refresh reads again.
	*now = now.Add(connectorsTTL - time.Second)
	if _, err := svc.Connectors(ctx, "work", false); err != nil {
		t.Fatal(err)
	}
	if got := len(provider.reads()); got != 1 {
		t.Fatalf("reads after a cached request = %d, want 1", got)
	}
	if _, err := svc.Connectors(ctx, "work", true); err != nil {
		t.Fatal(err)
	}
	if got := len(provider.reads()); got != 2 {
		t.Fatalf("reads after a refresh = %d, want 2", got)
	}

	// Past the TTL a plain request reads again.
	*now = now.Add(connectorsTTL + time.Second)
	if _, err := svc.Connectors(ctx, "work", false); err != nil {
		t.Fatal(err)
	}
	if got := len(provider.reads()); got != 3 {
		t.Fatalf("reads after the TTL = %d, want 3", got)
	}
}

// The computer's own login is read with no account folder, so the CLI uses
// its default config and the device credential.
func TestConnectorsReadsTheDeviceAccountWithoutAFolder(t *testing.T) {
	provider := &fakeConnectorClaude{}
	svc, _ := connectorTestService(t, provider)
	id := domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude)
	if _, err := svc.Connectors(context.Background(), id, false); err != nil {
		t.Fatal(err)
	}
	if got := provider.reads(); len(got) != 1 || got[0] != "" {
		t.Fatalf("device reads = %q, want one with an empty home", got)
	}
}

// A setup token or API key carries no claude.ai account, so no CLI runs and
// the reading says why (#9101).
func TestConnectorsSaysWhyAnAccountCannotLoadAny(t *testing.T) {
	provider := &fakeConnectorClaude{}
	svc, _ := connectorTestService(t, provider,
		domain.AgentAccount{ID: "token", Provider: domain.AgentAccountProviderClaude, Home: "/accounts/token", Kind: domain.AgentAccountKindSetupToken},
		domain.AgentAccount{ID: "key", Provider: domain.AgentAccountProviderClaude, Home: "/accounts/key", Kind: domain.AgentAccountKindAPIKey},
	)
	for id, want := range map[string]domain.AgentConnectorsUnavailable{
		"token": domain.AgentConnectorsSetupToken,
		"key":   domain.AgentConnectorsAPIKey,
	} {
		reading, err := svc.Connectors(context.Background(), id, true)
		if err != nil {
			t.Fatal(err)
		}
		if reading.Unavailable != want || len(reading.Connectors) != 0 {
			t.Fatalf("%s: reading = %+v, want unavailable %q", id, reading, want)
		}
	}
	if got := provider.reads(); len(got) != 0 {
		t.Fatalf("the CLI ran for accounts without connectors: %v", got)
	}
}

func TestConnectorsUnsupportedProvider(t *testing.T) {
	svc, _ := connectorTestService(t, &fakeClaude{})
	reading, err := svc.Connectors(context.Background(), domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude), false)
	if err != nil {
		t.Fatal(err)
	}
	if reading.Unavailable != domain.AgentConnectorsUnsupported {
		t.Fatalf("reading = %+v, want unsupported", reading)
	}
}

// A failed read is kept as the reading's error, not a request failure, so the
// tab shows it next to "Check again".
func TestConnectorsKeepsAFailedRead(t *testing.T) {
	provider := &fakeConnectorClaude{connectorErr: errors.New("claude: exit status 1")}
	svc, _ := connectorTestService(t, provider)
	reading, err := svc.Connectors(context.Background(), domain.DeviceAgentAccountID(domain.AgentAccountProviderClaude), false)
	if err != nil {
		t.Fatal(err)
	}
	if reading.Error == "" || reading.CheckedAt.IsZero() {
		t.Fatalf("reading = %+v, want the failure recorded", reading)
	}
}

func TestConnectorsUnknownAccount(t *testing.T) {
	svc, _ := connectorTestService(t, &fakeConnectorClaude{})
	if _, err := svc.Connectors(context.Background(), "missing", false); !errors.Is(err, ports.ErrAgentAccountUnknown) {
		t.Fatalf("error = %v, want unknown account", err)
	}
}
