package host

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers/openai"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	executor "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

type heldExecutor struct {
	fakeExecutor
	started chan string
	release chan struct{}
	stream  bool
}

func (e *heldExecutor) Execute(ctx context.Context, a *coreauth.Auth, req executor.Request, opts executor.Options) (executor.Response, error) {
	select {
	case e.started <- a.ID:
	case <-ctx.Done():
		return executor.Response{}, ctx.Err()
	}
	select {
	case <-e.release:
		return e.fakeExecutor.Execute(ctx, a, req, opts)
	case <-ctx.Done():
		return executor.Response{}, ctx.Err()
	}
}
func (e *heldExecutor) ExecuteStream(ctx context.Context, a *coreauth.Auth, _ executor.Request, _ executor.Options) (*executor.StreamResult, error) {
	e.mu.Lock()
	e.calls = append(e.calls, a.ID)
	e.mu.Unlock()
	select {
	case e.started <- a.ID:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	chunks := make(chan executor.StreamChunk, 2)
	chunks <- executor.StreamChunk{Payload: []byte(`data: {"type":"response.created","response":{"id":"held","object":"response","status":"in_progress","output":[]}}` + "\n\n")}
	go func() {
		defer close(chunks)
		select {
		case <-e.release:
			select {
			case chunks <- executor.StreamChunk{Payload: []byte(fmt.Sprintf("data: {\"type\":\"response.completed\",\"response\":{\"id\":%q,\"object\":\"response\",\"status\":\"completed\",\"output\":[]}}\n\n", a.ID))}:
			case <-ctx.Done():
			}
		case <-ctx.Done():
		}
	}()
	return &executor.StreamResult{Chunks: chunks}, nil
}
func heldRoutingServer(t *testing.T) (*httptest.Server, *Routes, *heldExecutor) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	routes := testRoutes(t)
	applyRoutes(t, routes, 1, testRoute("s-a", "ticket-a", "codex", "held-a"), testRoute("s-b", "ticket-b", "codex", "held-b"))
	manager := coreauth.NewManager(nil, exactSelector{}, nil)
	manager.SetConfig(&config.Config{})
	manager.SetRetryConfig(0, time.Millisecond, 1)
	execution := &heldExecutor{started: make(chan string, 8), release: make(chan struct{})}
	manager.RegisterExecutor(execution)
	for _, id := range []string{"held-a", "held-b"} {
		if _, err := manager.Register(coreauth.WithSkipPersist(context.Background()), &coreauth.Auth{ID: id, Provider: "codex", Status: coreauth.StatusActive}); err != nil {
			t.Fatal(err)
		}
		cliproxy.GlobalModelRegistry().RegisterClient(id, "codex", []*cliproxy.ModelInfo{{ID: "held-model"}})
		t.Cleanup(func() { cliproxy.GlobalModelRegistry().UnregisterClient(id) })
	}
	base := handlers.NewBaseAPIHandlers(&config.SDKConfig{}, manager)
	responses := openai.NewOpenAIResponsesAPIHandler(base)
	boundary := Boundary{Routes: routes, ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	engine := gin.New()
	engine.Use(boundary.Middleware)
	boundary.Configure(engine, base, &config.Config{})
	engine.POST("/v1/responses", responses.Responses)
	server := httptest.NewServer(engine)
	t.Cleanup(server.Close)
	return server, routes, execution
}

type heldResponse struct {
	status int
	body   string
	err    error
}

func requestHeld(t *testing.T, server *httptest.Server, ticket string, stream bool) (<-chan heldResponse, context.CancelFunc) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	output := make(chan heldResponse, 1)
	go func() {
		request, err := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":"held-model","input":"test","stream":%t}`, stream)))
		if err != nil {
			output <- heldResponse{err: err}
			return
		}
		request.Header.Set("Authorization", "Bearer "+ticket)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(accountHeader, "held-b")
		response, err := server.Client().Do(request)
		if err != nil {
			output <- heldResponse{err: err}
			return
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		output <- heldResponse{status: response.StatusCode, body: string(body), err: err}
	}()
	return output, cancel
}
func awaitAccount(t *testing.T, execution *heldExecutor, want string) {
	t.Helper()
	select {
	case got := <-execution.started:
		if got != want {
			t.Fatalf("request selected=%s want=%s", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("model request was not admitted")
	}
}
func awaitResponse(t *testing.T, result <-chan heldResponse, wantAccount string) {
	t.Helper()
	select {
	case got := <-result:
		if got.err != nil || got.status != http.StatusOK || !strings.Contains(got.body, wantAccount) {
			t.Fatalf("response=%+v expected-account=%s", got, wantAccount)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("model request did not finish")
	}
}
func TestAcceptedModelRequestsKeepTheirAccountDuringConcurrentRouteChanges(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream-%t", stream), func(t *testing.T) {
			server, routes, execution := heldRoutingServer(t)
			result, cancel := requestHeld(t, server, "ticket-a", stream)
			defer cancel()
			awaitAccount(t, execution, "held-a")
			before := routes.Snapshot()
			next := Snapshot{Revision: 2, Routes: []Route{testRoute("s-a", "ticket-a", "codex", "held-b"), testRoute("s-b", "ticket-b", "codex", "held-b")}}
			if err := routes.Apply(next); !errors.Is(err, ErrBusy) {
				t.Fatalf("active switch=%v", err)
			}
			if !reflect.DeepEqual(routes.Snapshot(), before) {
				t.Fatal("active switch partially published")
			}
			if err := routes.Apply(Snapshot{Revision: 2, Routes: []Route{before.Routes[0], testRoute("s-b", "ticket-b", "codex", "")}}); err != nil {
				t.Fatalf("unrelated account sign-out=%v", err)
			}
			during := routes.Snapshot()
			if during.Revision != 2 || during.Routes[0].AuthID != "held-a" || during.Routes[1].AuthID != "" {
				t.Fatalf("unrelated mutation=%+v", during)
			}
			if _, release, err := routes.Acquire("ticket-b"); err == nil {
				release()
				t.Fatal("signed-out ticket admitted model work")
			}
			close(execution.release)
			awaitResponse(t, result, "held-a")
			next.Revision = 3
			deadline := time.Now().Add(time.Second)
			for {
				err := routes.Apply(next)
				if err == nil {
					break
				}
				if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
					t.Fatalf("idle switch did not clear: %v", err)
				}
				time.Sleep(time.Millisecond)
			}
			route, release, err := routes.Acquire("ticket-a")
			if err != nil {
				t.Fatal(err)
			}
			if route.AuthID != "held-b" {
				t.Fatalf("new admission account=%s", route.AuthID)
			}
			release()
			release()
			if routes.Snapshot().Revision != 3 {
				t.Fatal("idle switch revision was lost")
			}
			if !reflect.DeepEqual(execution.recorded(), []string{"held-a"}) {
				t.Fatalf("accepted request rerouted=%v", execution.recorded())
			}
		})
	}
}
func TestConcurrentSessionsUseDistinctAccountsWithoutSerializingInference(t *testing.T) {
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream-%t", stream), func(t *testing.T) {
			server, routes, execution := heldRoutingServer(t)
			first, cancelFirst := requestHeld(t, server, "ticket-a", stream)
			defer cancelFirst()
			awaitAccount(t, execution, "held-a")
			second, cancelSecond := requestHeld(t, server, "ticket-b", stream)
			defer cancelSecond()
			awaitAccount(t, execution, "held-b")
			if err := routes.Apply(Snapshot{Revision: 2, Routes: []Route{testRoute("s-a", "ticket-a", "codex", "held-b"), testRoute("s-b", "ticket-b", "codex", "held-a")}}); !errors.Is(err, ErrBusy) {
				t.Fatalf("batch switch while active=%v", err)
			}
			if routes.Snapshot().Revision != 1 {
				t.Fatal("busy batch advanced effective revision")
			}
			close(execution.release)
			awaitResponse(t, first, "held-a")
			awaitResponse(t, second, "held-b")
			calls := execution.recorded()
			if len(calls) != 2 {
				t.Fatalf("inference calls=%v", calls)
			}
			seen := map[string]int{}
			for _, id := range calls {
				seen[id]++
			}
			if seen["held-a"] != 1 || seen["held-b"] != 1 {
				t.Fatalf("two sessions shared an account: %v", seen)
			}
		})
	}
}
func TestCancelledHTTPRequestReleasesAccountMutationFence(t *testing.T) {
	server, routes, execution := heldRoutingServer(t)
	result, cancel := requestHeld(t, server, "ticket-a", true)
	awaitAccount(t, execution, "held-a")
	cancel()
	select {
	case got := <-result:
		if got.err == nil {
			t.Fatalf("cancelled HTTP request completed normally: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancel did not close HTTP request")
	}
	deadline := time.Now().Add(2 * time.Second)
	next := Snapshot{Revision: 2, Routes: []Route{testRoute("s-a", "ticket-a", "codex", "held-b"), testRoute("s-b", "ticket-b", "codex", "held-b")}}
	for {
		err := routes.Apply(next)
		if err == nil {
			break
		}
		if !errors.Is(err, ErrBusy) || time.Now().After(deadline) {
			t.Fatalf("cancel retained accepted-request fence: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	route, release, err := routes.Acquire("ticket-a")
	if err != nil {
		t.Fatal(err)
	}
	if route.AuthID != "held-b" {
		t.Fatalf("after cancellation account=%s", route.AuthID)
	}
	release()
	if routes.Snapshot().Revision != 2 {
		t.Fatal("cancelled-request idle mutation was lost")
	}
	close(execution.release)
}
func TestPrivateRoutingAPIRefusesActiveBatchAndAcknowledgesIdleRetry(t *testing.T) {
	server, routes, execution := heldRoutingServer(t)
	result, cancel := requestHeld(t, server, "ticket-a", false)
	defer cancel()
	awaitAccount(t, execution, "held-a")
	next := Snapshot{Revision: 2, Routes: []Route{testRoute("s-a", "ticket-a", "codex", "held-b"), testRoute("s-b", "ticket-b", "codex", "held-b")}}
	send := func() heldResponse {
		body, err := json.Marshal(next)
		if err != nil {
			t.Fatal(err)
		}
		request, err := http.NewRequest(http.MethodPut, server.URL+"/ao/routes", strings.NewReader(string(body)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Authorization", "Bearer "+strings.Repeat("c", 32))
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		return heldResponse{status: response.StatusCode, body: string(data)}
	}
	busy := send()
	if busy.status != http.StatusConflict || !strings.Contains(busy.body, "SESSION_BUSY") {
		t.Fatalf("active API reply=%+v", busy)
	}
	if routes.Snapshot().Revision != 1 {
		t.Fatal("refused API batch changed routes")
	}
	close(execution.release)
	awaitResponse(t, result, "held-a")
	var idle heldResponse
	deadline := time.Now().Add(time.Second)
	for {
		idle = send()
		if idle.status == http.StatusOK {
			break
		}
		if idle.status != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("idle retry=%+v", idle)
		}
		time.Sleep(time.Millisecond)
	}
	var acknowledged Snapshot
	if err := json.Unmarshal([]byte(idle.body), &acknowledged); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(acknowledged, next) {
		t.Fatalf("acknowledged=%+v requested=%+v", acknowledged, next)
	}
	replay := send()
	if replay.status != http.StatusOK {
		t.Fatalf("exact retry=%+v", replay)
	}
	if !reflect.DeepEqual(execution.recorded(), []string{"held-a"}) {
		t.Fatal("routing control triggered extra model requests")
	}
}
func TestSessionTicketsCannotReadPrivateRoutesWhileOtherSessionsAreActive(t *testing.T) {
	server, routes, execution := heldRoutingServer(t)
	result, cancel := requestHeld(t, server, "ticket-a", false)
	defer cancel()
	awaitAccount(t, execution, "held-a")
	for _, ticket := range []string{"ticket-a", "ticket-b", strings.Repeat("i", 32), ""} {
		for _, path := range []string{"/ao/status", "/ao/login-result/attempt", "/v8/management/credentials", "/v8/management/oauth/status?state=private"} {
			request, err := http.NewRequest(http.MethodGet, server.URL+path, nil)
			if err != nil {
				t.Fatal(err)
			}
			request.Header.Set("Authorization", "Bearer "+ticket)
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			body, err := io.ReadAll(response.Body)
			response.Body.Close()
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != http.StatusUnauthorized {
				t.Fatalf("private route %s token=%q status=%d", path, ticket, response.StatusCode)
			}
			if strings.Contains(string(body), "held-a") || strings.Contains(string(body), "held-b") || strings.Contains(string(body), "ticket_hash") {
				t.Fatal("denied private request exposed route catalogue")
			}
		}
	}
	if routes.Snapshot().Revision != 1 {
		t.Fatal("unauthorized management changed effective routes")
	}
	close(execution.release)
	awaitResponse(t, result, "held-a")
}
func TestRoutingCatalogueSnapshotsRemainIndependentDuringRequests(t *testing.T) {
	_, routes, execution := heldRoutingServer(t)
	close(execution.release)
	var wg sync.WaitGroup
	failures := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			current := routes.Snapshot()
			current.Routes[0].AuthID = "tampered"
			selected, release, err := routes.Acquire("ticket-a")
			if err != nil {
				failures <- err
				return
			}
			if selected.AuthID != "held-a" {
				failures <- fmt.Errorf("snapshot copy changed request selection: %s", selected.AuthID)
			}
			release()
			release()
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	if got := routes.Snapshot(); got.Revision != 1 || got.Routes[0].AuthID != "held-a" {
		t.Fatalf("catalogue mutated by readers: %+v", got)
	}
}
