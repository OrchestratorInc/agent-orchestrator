package controllers_test

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func TestSpawnInitialAccountReachesSessionBoundary(t *testing.T) {
	for _, mode := range []string{"managed", "native"} {
		t.Run(mode, func(t *testing.T) {
			svc := &fakeSessionService{sessions: make(map[domain.SessionID]domain.Session)}
			router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{})
			selection := `{"mode":"native"}`
			if mode == "managed" {
				selection = `{"mode":"managed","accountId":"account-a"}`
			}
			request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"projectId":"initial","harness":"codex","mode":"tui","account":`+selection+`}`))
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusCreated {
				t.Fatalf("valid explicit selection status=%d body=%s", response.Code, response.Body.String())
			}
			encoded, err := json.Marshal(svc.lastSpawn)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(encoded, []byte(`"Mode":"`+mode+`"`)) || (mode == "managed" && !bytes.Contains(encoded, []byte(`"AccountID":"account-a"`))) {
				t.Fatal("explicit initial account was ignored before reaching the session boundary")
			}
		})
	}
}

func TestSpawnInitialAccountRejectsAmbiguousChoice(t *testing.T) {
	for name, fields := range map[string]string{
		"null":                `"account":null`,
		"empty":               `"account":{}`,
		"missing account":     `"account":{"mode":"managed"}`,
		"native with account": `"account":{"mode":"native","accountId":"account-a"}`,
		"unknown mode":        `"account":{"mode":"fallback","accountId":"account-a"}`,
		"unknown field":       `"account":{"mode":"native","credential":"synthetic"}`,
		"wrong type":          `"account":"account-a"`,
		"mode type":           `"account":{"mode":true}`,
		"nested alias":        `"account":{"Mode":"native"}`,
		"duplicate mode":      `"account":{"mode":"managed","mode":"native","accountId":"account-a"}`,
		"reverse mode":        `"account":{"mode":"native","mode":"managed","accountId":"account-a"}`,
		"duplicate account":   `"account":{"mode":"managed","accountId":"account-a","accountId":"account-b"}`,
		"top alias":           `"Account":{"mode":"managed","accountId":"account-a"}`,
		"top duplicate":       `"account":{"mode":"managed","accountId":"account-a"},"account":{"mode":"native"}`,
		"top reverse":         `"account":{"mode":"native"},"account":{"mode":"managed","accountId":"account-a"}`,
	} {
		t.Run(name, func(t *testing.T) {
			svc := &fakeSessionService{sessions: make(map[domain.SessionID]domain.Session)}
			router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{})
			request := httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"projectId":"initial","harness":"codex","mode":"tui",`+fields+`}`))
			request.Header.Set("X-Request-Id", "initial-choice-request")
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)
			if response.Code != http.StatusBadRequest {
				t.Errorf("ambiguous choice status=%d, want 400", response.Code)
			}
			if !strings.Contains(response.Body.String(), `"requestId":"initial-choice-request"`) {
				t.Error("validation lost the request ID")
			}
			if len(svc.sessions) != 0 || svc.lastSpawn.ProjectID != "" {
				t.Error("invalid account choice reached session creation")
			}
		})
	}
}

func TestSpawnInitialAccountOmittedRetainsCompatibility(t *testing.T) {
	svc := &fakeSessionService{sessions: make(map[domain.SessionID]domain.Session)}
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/sessions", strings.NewReader(`{"projectId":"initial","harness":"codex","mode":"tui"}`)))
	if response.Code != http.StatusCreated || len(svc.sessions) != 1 {
		t.Fatal("legacy spawn without an account choice changed")
	}
}

func TestDelegateInitialAccountReachesSessionBoundary(t *testing.T) {
	for _, input := range []string{
		`{"mode":"managed","accountId":"account-a"}`,
		`{"mode":"native"}`,
		`{"mode":"native","mode":"managed","accountId":"account-b"}`,
		`null`,
	} {
		t.Run(input, func(t *testing.T) {
			svc := &fakeSessionService{}
			router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{Sessions: svc}, httpd.ControlDeps{})
			response := httptest.NewRecorder()
			router.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/orchestrators/delegate", strings.NewReader(`{"projectId":"initial","agent":"codex","account":`+input+`}`)))
			valid := input == `{"mode":"managed","accountId":"account-a"}` || input == `{"mode":"native"}`
			if !valid {
				if response.Code != http.StatusBadRequest || svc.delegationInput.ProjectID != "" {
					t.Fatal("ambiguous delegated choice reached creation")
				}
				return
			}
			encoded, err := json.Marshal(svc.delegationInput)
			if err != nil || response.Code != http.StatusAccepted || !bytes.Contains(encoded, []byte(`"Account":{`)) {
				t.Fatal("delegation dropped the explicit initial choice", err)
			}
		})
	}
}

func TestInitialAccountCapabilityUnavailableWithoutSessionSupport(t *testing.T) {
	router := httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil, httpd.APIDeps{}, httpd.ControlDeps{})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/sessions/account-selection", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"initialSelection":false`) {
		t.Fatal("client cannot distinguish an unsupported daemon from an explicit native choice")
	}
}
