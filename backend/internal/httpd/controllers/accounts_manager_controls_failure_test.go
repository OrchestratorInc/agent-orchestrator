package controllers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	accountsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite/sqlitetest"
)

// Retry execution is covered by coordinator tests; this adapter returns its durable readback.
type durableFailureControls struct{ *persistedControlService }

func (s durableFailureControls) RetryAccountSwitch(ctx context.Context, sessionID domain.SessionID, id string) (domain.AccountsManagerSwitch, error) {
	return s.AccountSwitch(ctx, sessionID, id)
}

func (s durableFailureControls) RetryAccountRemoval(ctx context.Context, id string) (domain.AccountsManagerRemoval, error) {
	return s.AccountRemoval(ctx, id)
}

func (durableFailureControls) AccountSwitchCanRetry(domain.AccountsManagerSwitch) bool { return false }

func TestAccountControlDurableFailureProjection(t *testing.T) {
	for _, tc := range []struct{ switchCode, removalCode, wantSwitch, wantRemoval string }{
		{"TARGET_UNAVAILABLE", "SOURCE_STOP_UNCONFIRMED", "TARGET_UNAVAILABLE", "SOURCE_STOP_UNCONFIRMED"},
		{"TARGET_REVALIDATION_UNAVAILABLE", "REVOCATION_UNCONFIRMED", "TARGET_REVALIDATION_UNAVAILABLE", "REVOCATION_UNCONFIRMED"},
		{"DAEMON_RESTARTED", "CREDENTIAL_REMOVAL_UNCONFIRMED", "DAEMON_RESTARTED", "CREDENTIAL_REMOVAL_UNCONFIRMED"},
		{"private-error", "http://127.0.0.1:54321/secret-token", "", ""},
	} {
		t.Run(tc.switchCode, func(t *testing.T) {
			dir := t.TempDir()
			st, err := sqlitetest.Open(dir)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if st != nil {
					_ = st.Close()
				}
			})
			s := &persistedControlService{store: st, service: accountsvc.New(controlServiceCatalog{}, st)}
			binding := s.seedSession(t, "amc_a")
			base := "/sessions/" + string(binding.SessionID)
			body := fmt.Sprintf(`{"operationId":"switch-a","expectedRevision":%d,"mode":"managed","accountId":"amc_b","policy":"drain"}`, binding.Revision)
			if res := accountControlRequest(t, s, http.MethodPost, base+"/account-switches", body); res.Code != http.StatusAccepted {
				t.Fatalf("admit switch: %d %s", res.Code, res.Body.String())
			}
			beforeSwitch, err := st.AdvanceAccountsManagerSwitch(t.Context(), "switch-a", domain.AccountsManagerSwitchRequested, domain.AccountsManagerSwitchWaiting, tc.switchCode)
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := st.CreateAccountsManagerRemoval(t.Context(), "remove-a", "amc_c", 0, false); err != nil {
				t.Fatal(err)
			}
			if err := st.RecordAccountsManagerRemovalFailure(t.Context(), "remove-a", tc.removalCode); err != nil {
				t.Fatal(err)
			}
			beforeRemoval, _, err := st.GetAccountsManagerRemoval(t.Context(), "remove-a")
			if err != nil {
				t.Fatal(err)
			}
			if err := st.Close(); err != nil {
				t.Fatal(err)
			}
			st, err = sqlite.OpenPreMigrated(dir)
			if err != nil {
				t.Fatal(err)
			}
			controls := durableFailureControls{&persistedControlService{store: st, service: accountsvc.New(controlServiceCatalog{}, st)}}
			for _, route := range []struct{ method, path, nested, want string }{
				{http.MethodGet, base + "/account", "switch", tc.wantSwitch},
				{http.MethodGet, base + "/account-switches/switch-a", "", tc.wantSwitch},
				{http.MethodPost, base + "/account-switches/switch-a/retry", "", tc.wantSwitch},
				{http.MethodGet, "/accounts-manager/removals/remove-a", "", tc.wantRemoval},
				{http.MethodPost, "/accounts-manager/removals/remove-a/retry", "", tc.wantRemoval},
			} {
				res := accountControlRequest(t, controls, route.method, route.path, "{}")
				wantStatus := http.StatusOK
				if route.method == http.MethodPost {
					wantStatus = http.StatusAccepted
				}
				var response map[string]any
				if res.Code != wantStatus || json.Unmarshal(res.Body.Bytes(), &response) != nil {
					t.Fatalf("readback: %d %s", res.Code, res.Body.String())
				}
				if route.nested != "" {
					response = response[route.nested].(map[string]any)
				}
				got, exists := response["errorCode"]
				if (route.want != "" && got != route.want) || (route.want == "" && exists) {
					t.Errorf("%s %s lost or leaked durable code: got %v want %q", route.method, route.path, got, route.want)
				}
				if response["canRetry"] == true {
					t.Error("diagnostic granted retry without service capability")
				}
			}
			afterSwitch, _, _ := st.GetAccountsManagerSwitch(t.Context(), "switch-a")
			afterRemoval, _, _ := st.GetAccountsManagerRemoval(t.Context(), "remove-a")
			if !reflect.DeepEqual(beforeSwitch, afterSwitch) || !reflect.DeepEqual(beforeRemoval, afterRemoval) {
				t.Fatal("public projection mutated durable state")
			}
			res := accountControlRequest(t, controls, http.MethodGet, "/sessions/foreign/account-switches/switch-a", "")
			if res.Code != http.StatusNotFound || !json.Valid(res.Body.Bytes()) || !containsRequestID(res.Body.Bytes(), "control-test-request") {
				t.Fatalf("ownership error lost correlation: %d %s", res.Code, res.Body.String())
			}
		})
	}
}

func TestAccountControlFailureTaxonomy(t *testing.T) {
	for _, response := range []reflect.Type{reflect.TypeFor[AccountsManagerSwitchResponse](), reflect.TypeFor[AccountsManagerRemovalResponse]()} {
		field, ok := response.FieldByName("ErrorCode")
		if !ok || field.Tag.Get("enum") == "" {
			t.Fatal("public diagnostic taxonomy missing")
		}
		for _, code := range strings.Split(field.Tag.Get("enum"), ",") {
			if accountControlFailureCode("recovery_required", code) != code {
				t.Errorf("documented code not projected: %s", code)
			}
		}
	}
	for _, code := range []string{"private-error", " TARGET_UNAVAILABLE", "target_unavailable", "TARGET_UNAVAILABLE\n", "__proto__"} {
		if accountControlFailureCode("failed", code) != "" {
			t.Error("noncanonical code leaked")
		}
	}
}

func containsRequestID(data []byte, id string) bool {
	var body struct {
		RequestID string `json:"requestId"`
	}
	return json.Unmarshal(data, &body) == nil && body.RequestID == id
}

func TestAccountControlFailureProjectionTerminalAndMutation(t *testing.T) {
	for _, phase := range []domain.AccountsManagerSwitchPhase{domain.AccountsManagerSwitchFailed, domain.AccountsManagerSwitchReady, domain.AccountsManagerSwitchCancelled} {
		f := accountControlsFixture()
		f.switchOp.Phase, f.switchOp.ErrorCode = phase, "TARGET_UNAVAILABLE"
		res := accountControlRequest(t, f, http.MethodPost, "/sessions/session-a/account-switches", `{"operationId":"switch-a","expectedRevision":7,"mode":"managed","accountId":"amc_b","policy":"drain"}`)
		var body map[string]any
		if res.Code != http.StatusAccepted || json.Unmarshal(res.Body.Bytes(), &body) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		if phase == domain.AccountsManagerSwitchFailed && body["errorCode"] != "TARGET_UNAVAILABLE" {
			t.Error("accepted failed operation lost its diagnostic")
		}
		if phase != domain.AccountsManagerSwitchFailed && body["errorCode"] != nil {
			t.Error("terminal success or cancellation retained stale diagnostic")
		}
	}
	for _, phase := range []domain.AccountsManagerRemovalPhase{domain.AccountsManagerRemovalRecovery, domain.AccountsManagerRemovalComplete, domain.AccountsManagerRemovalCancelled} {
		f := accountControlsFixture()
		f.removal.Phase, f.removal.ErrorCode = phase, "REVOCATION_UNCONFIRMED"
		res := accountControlRequest(t, f, http.MethodPost, "/accounts-manager/accounts/amc_a/removals", `{"operationId":"removal-a","expectedRevision":9,"confirmed":true}`)
		var body map[string]any
		if res.Code != http.StatusAccepted || json.Unmarshal(res.Body.Bytes(), &body) != nil {
			t.Fatal(res.Code, res.Body.String())
		}
		if phase == domain.AccountsManagerRemovalRecovery && body["errorCode"] != "REVOCATION_UNCONFIRMED" {
			t.Error("accepted removal lost its diagnostic")
		}
		if phase != domain.AccountsManagerRemovalRecovery && body["errorCode"] != nil {
			t.Error("completed or cancelled removal retained stale diagnostic")
		}
	}
}
