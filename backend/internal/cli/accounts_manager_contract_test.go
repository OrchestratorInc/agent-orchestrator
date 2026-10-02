package cli

import (
	"context"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

type managedControlContract struct {
	controllers.AccountsManagerControls
	mu       sync.Mutex
	switchOp domain.AccountsManagerSwitch
	removal  domain.AccountsManagerRemoval
}

func (f *managedControlContract) SessionAccount(_ context.Context, id domain.SessionID) (domain.AccountsManagerSessionRoute, *domain.AccountsManagerSwitch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	op := f.switchOp
	return domain.AccountsManagerSessionRoute{SessionID: id, Provider: domain.AccountsManagerProviderCodex, Mode: domain.AccountsManagerManaged, AccountID: "amc_a", Revision: 7}, &op, nil
}

func (f *managedControlContract) StartAccountSwitch(_ context.Context, op domain.AccountsManagerSwitch) (domain.AccountsManagerSwitch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if op.SourceRevision != 7 {
		return domain.AccountsManagerSwitch{}, domain.ErrAccountsManagerSwitchConflict
	}
	op.SourceMode = domain.AccountsManagerManaged
	op.SourceAccountID = "amc_a"
	op.SourceRuntimeHandleID = "secret-marker"
	op.Phase = domain.AccountsManagerSwitchWaiting
	if f.switchOp.ID != "" && !op.SameRequest(f.switchOp) {
		return domain.AccountsManagerSwitch{}, domain.ErrAccountsManagerSwitchConflict
	}
	f.switchOp = op
	return op, nil
}

func (f *managedControlContract) AccountSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.switchOp, nil
}

func (f *managedControlContract) RetryAccountSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.switchOp.Phase == domain.AccountsManagerSwitchCancelled {
		return domain.AccountsManagerSwitch{}, domain.ErrAccountsManagerSwitchConflict
	}
	f.switchOp.Phase = domain.AccountsManagerSwitchRecoveryRequired
	return f.switchOp, nil
}

func (f *managedControlContract) CancelAccountSwitch(context.Context, domain.SessionID, string) (domain.AccountsManagerSwitch, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.switchOp.Phase = domain.AccountsManagerSwitchCancelled
	return f.switchOp, nil
}

func (f *managedControlContract) AccountRemovalImpact(context.Context, string) (domain.AccountsManagerRemovalImpact, error) {
	return domain.AccountsManagerRemovalImpact{Revision: 0, Sessions: []domain.AccountsManagerRemovalSession{{SessionID: "dormant-a", Provider: domain.AccountsManagerProviderCodex, BindingRevision: 7, RuntimeHandleID: "secret-marker"}}}, nil
}

func (f *managedControlContract) StartAccountRemoval(ctx context.Context, id, accountID string, revision int64, confirmed bool) (domain.AccountsManagerRemoval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if revision != 0 || !confirmed {
		return domain.AccountsManagerRemoval{}, domain.ErrAccountsManagerRemovalConflict
	}
	impact, _ := f.AccountRemovalImpact(ctx, accountID)
	f.removal = domain.AccountsManagerRemoval{ID: id, AccountID: accountID, Impact: impact, Phase: domain.AccountsManagerRemovalRequested}
	return f.removal, nil
}

func (f *managedControlContract) AccountRemoval(context.Context, string) (domain.AccountsManagerRemoval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.removal, nil
}

func (f *managedControlContract) RetryAccountRemoval(context.Context, string) (domain.AccountsManagerRemoval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removal.Phase = domain.AccountsManagerRemovalRecovery
	return f.removal, nil
}

func (f *managedControlContract) CancelAccountRemoval(context.Context, string) (domain.AccountsManagerRemoval, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.removal.Phase = domain.AccountsManagerRemovalCancelled
	return f.removal, nil
}

func TestSessionAccountHTTPContractRoundTrip(t *testing.T) {
	cfg := setConfigEnv(t)
	fixture := &managedControlContract{}
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{AccountsManagerControls: fixture}, httpd.ControlDeps{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	for _, tt := range []struct {
		args     []string
		contains string
		code     int
	}{
		{[]string{"session", "account", "switch", "session-a", "--account", "amc_b", "--expected-revision", "7", "--policy", "drain", "--operation-id", "switch-a"}, "waiting", 0},
		{[]string{"session", "account", "switch", "session-a", "--account", "amc_b", "--expected-revision", "7", "--policy", "drain", "--operation-id", "switch-a"}, "waiting", 0},
		{[]string{"session", "account", "get", "session-a"}, "amc_a", 0},
		{[]string{"session", "account", "status", "session-a", "switch-a"}, "waiting", 0},
		{[]string{"session", "account", "status", "session-other", "switch-a"}, "ACCOUNTS_MANAGER_CONTROL_NOT_FOUND", 1},
		{[]string{"session", "account", "switch", "session-a", "--account", "amc_b", "--expected-revision", "6", "--policy", "drain", "--operation-id", "stale-a"}, "ACCOUNTS_MANAGER_CONTROL_CONFLICT", 1},
		{[]string{"session", "account", "retry", "session-a", "switch-a"}, "recovery_required", 0},
		{[]string{"session", "account", "cancel", "session-a", "switch-a"}, "cancelled", 0},
		{[]string{"session", "account", "retry", "session-a", "switch-a"}, "ACCOUNTS_MANAGER_CONTROL_CONFLICT", 1},
		{[]string{"accounts", "removal-impact", "amc_a"}, "dormant-a", 0},
		{[]string{"accounts", "remove", "amc_a", "--operation-id", "remove-a", "--expected-revision", "0", "--confirm"}, "requested", 0},
		{[]string{"accounts", "removal-status", "amc_a", "remove-a"}, "requested", 0},
		{[]string{"accounts", "removal-retry", "amc_a", "remove-a"}, "recovery_required", 0},
		{[]string{"accounts", "removal-cancel", "amc_a", "remove-a"}, "cancelled", 0},
	} {
		t.Run(strings.Join(tt.args, " "), func(t *testing.T) {
			out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, append(tt.args, "--json")...)
			if ExitCode(err) != tt.code {
				t.Fatalf("exit=%d: %v", ExitCode(err), err)
			}
			text := out + errOut
			if err != nil {
				text += err.Error()
				if !strings.Contains(text, "[request ") {
					t.Fatalf("request ID lost: %v", err)
				}
			}
			if !strings.Contains(text, tt.contains) || strings.Contains(text, "secret-marker") {
				t.Fatalf("public contract: %s", text)
			}
		})
	}
}

func TestSessionAccountHTTPUnavailableDoesNotInferCapability(t *testing.T) {
	cfg := setConfigEnv(t)
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{}, httpd.ControlDeps{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	for _, args := range [][]string{{"session", "account", "get", "session-a"}, {"session", "account", "switch", "session-a", "--native", "--expected-revision", "7", "--policy", "interrupt", "--operation-id", "switch-a"}, {"accounts", "removal-impact", "amc_a"}} {
		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
		if ExitCode(err) != 1 || out != "" || !strings.Contains(err.Error(), "NOT_IMPLEMENTED") || !strings.Contains(err.Error(), "[request ") {
			t.Fatalf("unavailable dependency enabled: %v %s", err, out)
		}
	}
}
