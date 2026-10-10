package host

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/claude"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/openai"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type claudeExecutionCall struct{ account, kind string }
type claudeExecutorFixture struct {
	fakeExecutor
	mutex      sync.Mutex
	operations []claudeExecutionCall
	failure    int
}

func (*claudeExecutorFixture) Identifier() string { return "claude" }
func (e *claudeExecutorFixture) observe(a *coreauth.Auth, kind string) error {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	e.operations = append(e.operations, claudeExecutionCall{account: a.ID, kind: kind})
	if e.failure != 0 {
		return &coreauth.Error{Code: "fixture-upstream-refused", HTTPStatus: e.failure, Message: "fake upstream failure"}
	}
	return nil
}
func (e *claudeExecutorFixture) Execute(_ context.Context, a *coreauth.Auth, _ executor.Request, _ executor.Options) (executor.Response, error) {
	if err := e.observe(a, "messages"); err != nil {
		return executor.Response{}, err
	}
	return executor.Response{Payload: []byte(fmt.Sprintf(`{"id":%q,"type":"message","role":"assistant","content":[{"type":"text","text":"fixture"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1}}`, a.ID))}, nil
}
func (e *claudeExecutorFixture) ExecuteStream(_ context.Context, a *coreauth.Auth, _ executor.Request, _ executor.Options) (*executor.StreamResult, error) {
	if err := e.observe(a, "stream"); err != nil {
		return nil, err
	}
	chunks := make(chan executor.StreamChunk, 2)
	chunks <- executor.StreamChunk{Payload: []byte(fmt.Sprintf("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"usage\":{\"input_tokens\":1,\"output_tokens\":0}}}\n\n", a.ID))}
	chunks <- executor.StreamChunk{Payload: []byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")}
	close(chunks)
	return &executor.StreamResult{Chunks: chunks}, nil
}
func (e *claudeExecutorFixture) CountTokens(_ context.Context, a *coreauth.Auth, _ executor.Request, _ executor.Options) (executor.Response, error) {
	if err := e.observe(a, "count"); err != nil {
		return executor.Response{}, err
	}
	return executor.Response{Payload: []byte(`{"input_tokens":3}`)}, nil
}
func (e *claudeExecutorFixture) recordedOperations() []claudeExecutionCall {
	e.mutex.Lock()
	defer e.mutex.Unlock()
	return append([]claudeExecutionCall(nil), e.operations...)
}
func claudeRoutingFixture(t *testing.T, failure int) (*gin.Engine, *Routes, *claudeExecutorFixture, *coreauth.Manager) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	manager := coreauth.NewManager(nil, exactSelector{}, nil)
	manager.SetConfig(&config.Config{})
	manager.SetRetryConfig(2, time.Millisecond, 3)
	executor := &claudeExecutorFixture{failure: failure}
	manager.RegisterExecutor(executor)
	for _, id := range []string{"claude-a", "claude-b"} {
		if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: id, Provider: "claude", Status: coreauth.StatusActive}); err != nil {
			t.Fatal(err)
		}
		cliproxy.GlobalModelRegistry().RegisterClient(id, "claude", []*cliproxy.ModelInfo{{ID: "claude-probe"}})
		t.Cleanup(func() { cliproxy.GlobalModelRegistry().UnregisterClient(id) })
	}
	routes := testRoutes(t)
	applyRoutes(t, routes, 1, testRoute("s-a", "claude-ticket-a", "claude", "claude-a"), testRoute("s-b", "claude-ticket-b", "claude", "claude-b"), testRoute("s-wait", "claude-waiting", "claude", ""), testRoute("s-codex", "codex-ticket", "codex", "codex-a"))
	base := handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager)
	claudeHandler := claude.NewClaudeCodeAPIHandler(base)
	codexHandler := openai.NewOpenAIResponsesAPIHandler(base)
	router := gin.New()
	router.Use(Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}.Middleware)
	router.POST("/v1/messages", claudeHandler.ClaudeMessages)
	router.POST("/v1/messages/count_tokens", claudeHandler.ClaudeCountTokens)
	router.POST("/v1/responses", codexHandler.Responses)
	router.POST("/v1/responses/compact", codexHandler.Compact)
	return router, routes, executor, manager
}
func sendClaudeFixture(router *gin.Engine, ticket, path string, stream, bearer bool) *httptest.ResponseRecorder {
	body := fmt.Sprintf(`{"model":"claude-probe","max_tokens":8,"messages":[{"role":"user","content":"fixture"}],"stream":%t}`, stream)
	request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if bearer {
		request.Header.Set("Authorization", "Bearer "+ticket)
	} else {
		request.Header.Set("X-Api-Key", ticket)
	}
	request.Header.Set("X-AO-Auth-ID", "claude-b")
	request.Header.Set("X-AO-Provider", "codex")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}
func TestClaudePublicSDKMessagesAndTokenCountsSelectExactSession(t *testing.T) {
	cases := []struct {
		name, path, kind string
		stream, bearer   bool
	}{
		{"messages-api-key", "/v1/messages", "messages", false, false},
		{"messages-bearer", "/v1/messages", "messages", false, true},
		{"stream-api-key", "/v1/messages", "stream", true, false},
		{"stream-bearer", "/v1/messages", "stream", true, true},
		{"count-api-key", "/v1/messages/count_tokens", "count", false, false},
		{"count-bearer", "/v1/messages/count_tokens", "count", false, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router, _, execution, _ := claudeRoutingFixture(t, 0)
			for _, ticket := range []string{"claude-ticket-a", "claude-ticket-b", "claude-ticket-a"} {
				response := sendClaudeFixture(router, ticket, tc.path, tc.stream, tc.bearer)
				if response.Code != http.StatusOK {
					t.Fatalf("Claude SDK status=%d response=%s", response.Code, response.Body.String())
				}
				if tc.stream && !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
					t.Fatal("Claude stream lost SSE content type")
				}
				if tc.kind == "count" {
					var count struct {
						InputTokens int `json:"input_tokens"`
					}
					if err := json.Unmarshal(response.Body.Bytes(), &count); err != nil || count.InputTokens != 3 {
						t.Fatalf("token count=%s err=%v", response.Body.String(), err)
					}
				}
			}
			want := []claudeExecutionCall{{"claude-a", tc.kind}, {"claude-b", tc.kind}, {"claude-a", tc.kind}}
			if !reflect.DeepEqual(execution.recordedOperations(), want) {
				t.Fatalf("Claude account selection=%v want=%v", execution.recordedOperations(), want)
			}
		})
	}
}
func TestClaudePublicSDKNeverFallsBackAfterProviderRefusal(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		for _, kind := range []string{"messages", "stream", "count"} {
			t.Run(fmt.Sprintf("%d/%s", status, kind), func(t *testing.T) {
				router, _, execution, _ := claudeRoutingFixture(t, status)
				path := "/v1/messages"
				if kind == "count" {
					path += "/count_tokens"
				}
				response := sendClaudeFixture(router, "claude-ticket-a", path, kind == "stream", false)
				if response.Code == http.StatusOK {
					t.Fatal("failed pinned account reported successful provider response")
				}
				calls := execution.recordedOperations()
				if len(calls) == 0 {
					t.Fatal("refusal fixture never reached pinned account")
				}
				for _, call := range calls {
					if call.account != "claude-a" {
						t.Fatalf("refusal fell back to another account=%v", calls)
					}
				}
			})
		}
	}
}
func TestClaudeUnavailableRoutesNeverInvokeAnotherProvider(t *testing.T) {
	for _, ticket := range []string{"unknown", "claude-waiting", "codex-ticket"} {
		for _, path := range []string{"/v1/messages", "/v1/messages/count_tokens"} {
			t.Run(ticket+path, func(t *testing.T) {
				router, _, execution, _ := claudeRoutingFixture(t, 0)
				response := sendClaudeFixture(router, ticket, path, false, false)
				if response.Code != http.StatusUnauthorized && response.Code != http.StatusBadRequest {
					t.Fatalf("unavailable route status=%d", response.Code)
				}
				if len(execution.recordedOperations()) != 0 {
					t.Fatal("unavailable or different-provider ticket reached Claude executor")
				}
			})
		}
	}
}
func TestClaudeTicketCannotUseCodexResponsesOrCompaction(t *testing.T) {
	router, _, execution, _ := claudeRoutingFixture(t, 0)
	for _, path := range []string{"/v1/responses", "/v1/responses/compact"} {
		response := sendClaudeFixture(router, "claude-ticket-a", path, false, true)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("different-provider endpoint=%s status=%d", path, response.Code)
		}
	}
	if len(execution.recordedOperations()) != 0 {
		t.Fatal("Claude ticket executed through Codex endpoint")
	}
}
func TestClaudeManualSwitchKeepsTicketAndChangesOnlyItsAccount(t *testing.T) {
	router, routes, execution, _ := claudeRoutingFixture(t, 0)
	first := sendClaudeFixture(router, "claude-ticket-a", "/v1/messages", false, false)
	if first.Code != http.StatusOK {
		t.Fatal(first.Body.String())
	}
	next := routes.Snapshot()
	next.Revision++
	for i, r := range next.Routes {
		if r.SessionID == "s-a" {
			next.Routes[i].AuthID = "claude-b"
		}
	}
	if err := routes.Apply(next); err != nil {
		t.Fatal(err)
	}
	for _, ticket := range []string{"claude-ticket-a", "claude-ticket-b"} {
		response := sendClaudeFixture(router, ticket, "/v1/messages", false, false)
		if response.Code != http.StatusOK {
			t.Fatal(response.Body.String())
		}
	}
	want := []claudeExecutionCall{{"claude-a", "messages"}, {"claude-b", "messages"}, {"claude-b", "messages"}}
	if !reflect.DeepEqual(execution.recordedOperations(), want) {
		t.Fatalf("switched Claude route=%v", execution.recordedOperations())
	}
	waiting := routes.Snapshot()
	waiting.Revision++
	for i, r := range waiting.Routes {
		if r.SessionID == "s-a" {
			waiting.Routes[i].AuthID = ""
		}
	}
	if err := routes.Apply(waiting); err != nil {
		t.Fatal(err)
	}
	response := sendClaudeFixture(router, "claude-ticket-a", "/v1/messages", false, false)
	if response.Code != http.StatusUnauthorized || len(execution.recordedOperations()) != 3 {
		t.Fatal("signed-out Claude ticket fell back to another account")
	}
}
