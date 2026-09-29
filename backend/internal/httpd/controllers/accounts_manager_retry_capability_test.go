package controllers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type retryCapabilityControls struct {
	*fakeAccountControls
	available bool
	observed  []domain.AccountsManagerSwitch
}

func TestAccountControlRetryCapabilityProductionService(t *testing.T) {
	s := newPersistedControlService(t)
	binding := s.seedSession(t, "amc_a")
	path := "/sessions/" + string(binding.SessionID)
	body := fmt.Sprintf(`{"operationId":"switch-recovery","expectedRevision":%d,"mode":"managed","accountId":"amc_b","policy":"drain"}`, binding.Revision)
	if res := accountControlRequest(t, s, http.MethodPost, path+"/account-switches", body); res.Code != http.StatusAccepted {
		t.Fatalf("seed operation: %d %s", res.Code, res.Body.String())
	}
	service := sessionsvc.NewWithDeps(sessionsvc.Deps{Store: s.store, Manager: s.manager})
	read := func(want bool) {
		t.Helper()
		res := accountControlRequest(t, service, http.MethodGet, path+"/account", "")
		var view struct {
			AccountID string `json:"accountId"`
			Switch    struct {
				CanRetry *bool  `json:"canRetry"`
				ID       string `json:"id"`
			} `json:"switch"`
		}
		if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &view) != nil || view.Switch.CanRetry == nil || *view.Switch.CanRetry != want || view.Switch.ID != "switch-recovery" || view.AccountID != "amc_a" {
			t.Fatalf("recovery projection: %d %s", res.Code, res.Body.String())
		}
	}
	read(false)
	if err := s.manager.ReconcileStartupSafety(t.Context()); err != nil {
		t.Fatal(err)
	}
	read(true)
	if res := accountControlRequest(t, service, http.MethodPost, path+"/account-switches/switch-recovery/cancel", "{}"); res.Code != http.StatusAccepted {
		t.Fatalf("cancel: %d %s", res.Code, res.Body.String())
	}
	read(false)
}

func TestAccountControlRetryCapabilityUnavailable(t *testing.T) {
	res := accountControlRequest(t, accountControlsFixture(), http.MethodGet, "/sessions/session-a/account-switches/switch-a", "")
	var view struct {
		CanRetry *bool `json:"canRetry"`
	}
	if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &view) != nil || view.CanRetry == nil || *view.CanRetry {
		t.Fatalf("unsupported capability did not fail closed: %d %s", res.Code, res.Body.String())
	}
}

func (c *retryCapabilityControls) AccountSwitchCanRetry(op domain.AccountsManagerSwitch) bool {
	c.observed = append(c.observed, op)
	return c.available
}

func TestAccountControlRetryCapability(t *testing.T) {
	for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, domain.AccountsManagerSwitchRecoveryRequired} {
		for _, available := range []bool{false, true} {
			for _, endpoint := range []string{"account", "account-switches/switch-a"} {
				t.Run(string(phase)+"/"+endpoint+"/"+map[bool]string{true: "idle", false: "running"}[available], func(t *testing.T) {
					controls := &retryCapabilityControls{fakeAccountControls: accountControlsFixture(), available: available}
					controls.switchOp.Phase = phase
					res := accountControlRequest(t, controls, http.MethodGet, "/sessions/session-a/"+endpoint, "")
					var body map[string]json.RawMessage
					if res.Code != http.StatusOK || json.Unmarshal(res.Body.Bytes(), &body) != nil {
						t.Fatalf("read=%d %s", res.Code, res.Body.String())
					}
					if endpoint == "account" {
						var nested map[string]json.RawMessage
						if err := json.Unmarshal(body["switch"], &nested); err != nil {
							t.Fatal(err)
						}
						body = nested
					}
					var got bool
					if err := json.Unmarshal(body["canRetry"], &got); err != nil || got != available {
						t.Fatalf("retry observation missing or incorrect: got=%s want=%t error=%v", body["canRetry"], available, err)
					}
					if len(controls.observed) != 1 || controls.observed[0] != controls.switchOp || len(controls.calls) != 1 {
						t.Fatal("retry observation changed identity or performed a mutation")
					}
				})
			}
		}
	}
}
