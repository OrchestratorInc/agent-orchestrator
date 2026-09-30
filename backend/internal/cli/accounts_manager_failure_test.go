package cli

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
)

func TestAccountDurableFailureOutput(t *testing.T) {
	for _, kind := range []string{"switch", "removal", "session"} {
		for _, asJSON := range []bool{false, true} {
			for _, tc := range []struct{ name, phase, code, want string }{
				{"known", "recovery_required", "REVOCATION_UNCONFIRMED", "REVOCATION_UNCONFIRMED"},
				{"cold", "waiting", "TARGET_REVALIDATION_UNAVAILABLE", "TARGET_REVALIDATION_UNAVAILABLE"},
				{"old-server", "recovery_required", "", ""},
				{"unknown", "recovery_required", "private-token http://127.0.0.1:54321", ""},
				{"cancelled", "cancelled", "REVOCATION_UNCONFIRMED", ""},
				{"ready", "ready", "REVOCATION_UNCONFIRMED", ""},
				{"complete", "complete", "REVOCATION_UNCONFIRMED", ""},
			} {
				if (kind == "removal" && tc.phase == "ready") || (kind != "removal" && tc.phase == "complete") {
					continue
				}
				if kind == "removal" && tc.phase == "waiting" {
					tc.phase = "recovery_required"
				}
				t.Run(kind+"/"+tc.name+map[bool]string{true: "/json", false: "/human"}[asJSON], func(t *testing.T) {
					cfg := setConfigEnv(t)
					op := map[string]any{"id": "op-a", "sessionId": "session-a", "accountId": "amc_a", "phase": tc.phase, "canRetry": false, "impact": map[string]any{"accountId": "amc_a", "revision": 0, "sessions": []any{}}}
					if tc.code != "" {
						op["errorCode"] = tc.code
					}
					var response any = op
					args := []string{"session", "account", "status", "session-a", "op-a"}
					if kind == "session" {
						args = []string{"session", "account", "get", "session-a"}
						response = map[string]any{"sessionId": "session-a", "mode": "managed", "accountId": "amc_a", "revision": 7, "switch": op}
					} else if kind == "removal" {
						args = []string{"accounts", "removal-status", "amc_a", "op-a"}
					}
					server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
						if r.URL.Path == "/internal/telemetry/cli-invoked" {
							w.WriteHeader(http.StatusNoContent)
							return
						}
						if r.Method != http.MethodGet {
							t.Error("diagnostic initiated a mutation")
						}
						w.Header().Set("Content-Type", "application/json")
						_ = json.NewEncoder(w).Encode(response)
					}))
					t.Cleanup(server.Close)
					writeRunFileFor(t, cfg, server)
					if asJSON {
						args = append(args, "--json")
					}
					out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
					if err != nil {
						t.Fatal(err)
					}
					if tc.want != "" && !strings.Contains(out, tc.want) {
						t.Errorf("durable failure code missing from output: %s", out)
					}
					if tc.want == "" && (strings.Contains(out, "errorCode") || strings.Contains(out, "error code:") || (tc.code != "" && strings.Contains(out, tc.code))) {
						t.Errorf("private or stale diagnostic retained: %s", out)
					}
					if asJSON && !json.Valid([]byte(out)) {
						t.Fatal("invalid JSON output")
					}
				})
			}
		}
	}
}

func TestAccountDurableFailureSchemaParity(t *testing.T) {
	var taxonomy string
	for _, response := range []reflect.Type{reflect.TypeFor[controllers.AccountsManagerSwitchResponse](), reflect.TypeFor[controllers.AccountsManagerRemovalResponse]()} {
		field, ok := response.FieldByName("ErrorCode")
		if !ok || field.Tag.Get("json") != "errorCode,omitempty" || field.Tag.Get("enum") == "" {
			t.Fatal("diagnostic must be optional and bounded in the public contract")
		}
		if taxonomy != "" && taxonomy != field.Tag.Get("enum") {
			t.Fatal("switch and removal taxonomy differ")
		}
		taxonomy = field.Tag.Get("enum")
		for _, code := range strings.Split(taxonomy, ",") {
			if accountControlFailureCode("recovery_required", code) != code {
				t.Errorf("CLI discards documented code %s", code)
			}
			for _, phase := range []string{"ready", "complete", "cancelled"} {
				if accountControlFailureCode(phase, code) != "" {
					t.Errorf("stale diagnostic on %s", phase)
				}
			}
		}
	}
	for _, code := range []string{"private-token", " TARGET_UNAVAILABLE", "target_unavailable", "TARGET_UNAVAILABLE\n", "__proto__"} {
		if accountControlFailureCode("failed", code) != "" {
			t.Error("unknown diagnostic accepted")
		}
	}
}

func TestAccountDurableFailureActualRouter(t *testing.T) {
	cfg := setConfigEnv(t)
	fixture := &managedControlContract{
		switchOp: domain.AccountsManagerSwitch{ID: "switch-a", SessionID: "session-a", Phase: domain.AccountsManagerSwitchRecoveryRequired, ErrorCode: "TARGET_REVALIDATION_UNAVAILABLE"},
		removal:  domain.AccountsManagerRemoval{ID: "remove-a", AccountID: "amc_a", Phase: domain.AccountsManagerRemovalRecovery, ErrorCode: "SOURCE_STOP_UNCONFIRMED"},
	}
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{AccountsManagerControls: fixture}, httpd.ControlDeps{})
	server := httptest.NewServer(router)
	t.Cleanup(server.Close)
	writeRunFileFor(t, cfg, server)
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"session", "account", "get", "session-a"}, "TARGET_REVALIDATION_UNAVAILABLE"},
		{[]string{"session", "account", "status", "session-a", "switch-a", "--json"}, "TARGET_REVALIDATION_UNAVAILABLE"},
		{[]string{"session", "account", "retry", "session-a", "switch-a"}, "TARGET_REVALIDATION_UNAVAILABLE"},
		{[]string{"session", "account", "cancel", "session-a", "switch-a", "--json"}, ""},
		{[]string{"accounts", "removal-status", "amc_a", "remove-a"}, "SOURCE_STOP_UNCONFIRMED"},
		{[]string{"accounts", "removal-retry", "amc_a", "remove-a", "--json"}, "SOURCE_STOP_UNCONFIRMED"},
		{[]string{"accounts", "removal-cancel", "amc_a", "remove-a"}, ""},
	} {
		out, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, tc.args...)
		if err != nil || (tc.want != "" && !strings.Contains(out, tc.want)) {
			t.Fatalf("router diagnostic lost: %v %s", err, out)
		}
		if tc.want == "" && (strings.Contains(out, "UNCONFIRMED") || strings.Contains(out, "UNAVAILABLE") || strings.Contains(out, "errorCode")) {
			t.Fatal("cancellation retained stale diagnostic", out)
		}
	}
	_, _, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, "session", "account", "status", "foreign", "switch-a")
	if ExitCode(err) != 1 || !strings.Contains(err.Error(), "[request ") {
		t.Fatalf("ownership error lost request ID: %v", err)
	}
}
