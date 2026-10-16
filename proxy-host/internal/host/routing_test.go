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
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/executor"
)

// twoProviders has two accounts per provider, each with its own session, plus
// a session whose account is not signed in on the helper.
func twoProviders(t *testing.T, codex, anthropic *fakeProvider) *fixture {
	t.Helper()
	codex.name, anthropic.name = "codex", "claude"
	f := newFixture(t, codex, anthropic)
	for _, id := range []string{"codex-a", "codex-b"} {
		f.account(&coreauth.Auth{ID: id, Provider: "codex"})
	}
	for _, id := range []string{"claude-a", "claude-b"} {
		f.account(&coreauth.Auth{ID: id, Provider: "claude"})
	}
	f.route("codex-ticket-a codex codex-a", "codex-ticket-b codex codex-b", "claude-ticket-a claude claude-a", "claude-ticket-b claude claude-b", "codex-ticket-gone codex gone")
	return f
}

func TestSessionsRunOnExactlyTheirOwnAccount(t *testing.T) {
	for _, tc := range []struct {
		name, provider, path, kind string
		stream, apiKey             bool
	}{
		{"codex responses", "codex", "/v1/responses", "plain", false, false},
		{"codex stream", "codex", "/v1/responses", "stream", true, false},
		{"codex compact", "codex", "/v1/responses/compact", "plain", false, false},
		{"claude messages", "claude", "/v1/messages", "plain", false, false},
		{"claude stream", "claude", "/v1/messages", "stream", true, false},
		{"claude count", "claude", "/v1/messages/count_tokens", "count", false, false},
		{"claude x-api-key", "claude", "/v1/messages", "plain", false, true},
		{"claude x-api-key stream", "claude", "/v1/messages", "stream", true, true},
		{"claude x-api-key count", "claude", "/v1/messages/count_tokens", "count", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			codex, anthropic := &fakeProvider{}, &fakeProvider{}
			f := twoProviders(t, codex, anthropic)
			f.apiKey = tc.apiKey
			for _, account := range []string{"a", "b", "a"} {
				response := f.model(tc.path, tc.provider+"-ticket-"+account, tc.stream)
				if response.Code != http.StatusOK {
					t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
				}
				if tc.stream && !strings.Contains(response.Header().Get("Content-Type"), "text/event-stream") {
					t.Fatal("the stream lost its content type")
				}
			}
			want := []string{tc.provider + "-a " + tc.kind, tc.provider + "-b " + tc.kind, tc.provider + "-a " + tc.kind}
			got, idle := codex.ran(), anthropic.ran()
			if tc.provider == "claude" {
				got, idle = idle, got
			}
			if !reflect.DeepEqual(got, want) || len(idle) != 0 {
				t.Fatalf("ran on %v and %v, want %v", got, idle, want)
			}
		})
	}
}

func TestProviderRefusalNeverFallsBackToAnotherAccount(t *testing.T) {
	for _, status := range []int{http.StatusUnauthorized, http.StatusTooManyRequests, http.StatusInternalServerError, http.StatusServiceUnavailable} {
		for _, tc := range []struct {
			provider, path string
			stream         bool
		}{{"codex", "/v1/responses", false}, {"codex", "/v1/responses", true}, {"claude", "/v1/messages", false}, {"claude", "/v1/messages", true}, {"claude", "/v1/messages/count_tokens", false}} {
			t.Run(fmt.Sprintf("%d %s stream=%t", status, tc.path, tc.stream), func(t *testing.T) {
				codex, anthropic := &fakeProvider{failure: status}, &fakeProvider{failure: status}
				f := twoProviders(t, codex, anthropic)
				if response := f.model(tc.path, tc.provider+"-ticket-a", tc.stream); response.Code == http.StatusOK {
					t.Fatalf("a refused request succeeded: %s", response.Body.String())
				}
				calls := append(codex.ran(), anthropic.ran()...)
				if len(calls) == 0 {
					t.Fatal("the refusal never reached the session's account")
				}
				for _, call := range calls {
					if !strings.HasPrefix(call, tc.provider+"-a ") {
						t.Fatalf("the refusal fell back to another account: %v", calls)
					}
				}
			})
		}
	}
}

// A model the account's plan refuses is answered by the provider every time. Rested on the account instead, it made every later
// request fail at once with a retryable 503, which Codex retried.
func TestARefusedModelIsAnsweredByTheProviderEveryTime(t *testing.T) {
	for _, status := range []int{http.StatusBadRequest, http.StatusNotFound, http.StatusUnprocessableEntity} {
		codex := &fakeProvider{failure: status, refusal: "The '" + testModel + "' model is not supported when using Codex with a ChatGPT account."}
		f := twoProviders(t, codex, &fakeProvider{})
		for attempt := 0; attempt < 3; attempt++ {
			before := len(codex.ran())
			response := f.model("/v1/responses", "codex-ticket-a", false)
			if response.Code != status || !strings.Contains(response.Body.String(), "not supported when using Codex") || len(codex.ran()) == before {
				t.Fatalf("%d, request %d: %d %s after %d calls", status, attempt, response.Code, response.Body.String(), len(codex.ran()))
			}
		}
	}
}

// While the account rests, as after a used-up limit, its session hears the refusal that started the rest, as the provider worded it;
// a session whose account is gone still hears that it is unavailable.
func TestARestingAccountAnswersWithTheRefusalThatRestedIt(t *testing.T) {
	codex := &fakeProvider{failure: http.StatusTooManyRequests, refusal: "You've hit your usage limit. Try again in 3 hours."}
	f := twoProviders(t, codex, &fakeProvider{})
	for attempt := 0; attempt < 3; attempt++ {
		if response := f.model("/v1/responses", "codex-ticket-a", false); response.Code != http.StatusTooManyRequests || !strings.Contains(response.Body.String(), "hit your usage limit") {
			t.Fatalf("request %d: %d %s", attempt, response.Code, response.Body.String())
		}
	}
	if calls := codex.ran(); len(calls) != 1 {
		t.Fatalf("the resting account was asked again: %v", calls)
	}
	if response := f.model("/v1/responses", "codex-ticket-gone", false); response.Code != http.StatusServiceUnavailable {
		t.Fatalf("a missing account: %d %s", response.Code, response.Body.String())
	}
}

func TestOnlyARefusalOfTheRequestIsKeptOffTheAccount(t *testing.T) {
	refusal := func(status int, message string) coreauth.Result {
		return coreauth.Result{AuthID: "a", Model: "m", Error: &coreauth.Error{Code: "upstream", Message: message, HTTPStatus: status}}
	}
	credentialWide := refusal(http.StatusBadRequest, "plan limit")
	credentialWide.CredentialScope = true
	for name, tc := range map[string]struct {
		result coreauth.Result
		scoped bool
	}{
		"a model the plan refuses":     {refusal(http.StatusBadRequest, "The 'gpt-6.1-sol' model is not supported when using Codex with a ChatGPT account."), true},
		"an unknown model":             {refusal(http.StatusNotFound, "model not found"), true},
		"a request the provider can't": {refusal(http.StatusUnprocessableEntity, "unprocessable"), true},
		"a dead sign-in":               {refusal(http.StatusBadRequest, `{"error":"invalid_grant"}`), false},
		"a limit across the account":   {credentialWide, false},
		"a used-up limit":              {refusal(http.StatusTooManyRequests, "usage limit"), false},
		"a refused sign-in":            {refusal(http.StatusUnauthorized, "unauthorized"), false},
		"a provider failure":           {refusal(http.StatusInternalServerError, "oops"), false},
		"a success":                    {coreauth.Result{AuthID: "a", Model: "m", Success: true}, false},
	} {
		before := fmt.Sprintf("%+v", tc.result.Error)
		got := requestRefusal(context.Background(), tc.result)
		if (got.Error != nil && got.Error.IsRequestScoped()) != tc.scoped || fmt.Sprintf("%+v", tc.result.Error) != before {
			t.Fatalf("%s: %+v, scoped %v, original now %+v", name, got.Error, tc.scoped, tc.result.Error)
		}
		if tc.scoped && (got.Error.Message != tc.result.Error.Message || got.Error.HTTPStatus != tc.result.Error.HTTPStatus) {
			t.Fatalf("%s: the provider's answer changed: %+v", name, got.Error)
		}
	}
}

func TestSelectorNamesTheRefusalBehindARestAndOtherwiseTheAccountUnavailable(t *testing.T) {
	m := coreauth.NewManager(nil, nil, nil)
	later, earlier := time.Now().Add(time.Hour), time.Now().Add(-time.Minute)
	rest := func(model string, until time.Time, message string) map[string]*coreauth.ModelState {
		return map[string]*coreauth.ModelState{model: {Unavailable: true, NextRetryAfter: until, LastError: &coreauth.Error{Code: "quota", Message: message, HTTPStatus: 429}}}
	}
	for _, a := range []*coreauth.Auth{
		{ID: "resting", Provider: "codex", ModelStates: rest("model", later, `{"error":{"message":"usage limit reached"}}`)},
		{ID: "account-wide", Provider: "codex", LastError: &coreauth.Error{Message: "five-hour limit", HTTPStatus: 429}, Quota: coreauth.QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: later}},
		{ID: "rested-before", Provider: "codex", ModelStates: rest("model", earlier, "old limit")},
		{ID: "other-model", Provider: "codex", ModelStates: rest("another", later, "limit of another model")},
		{ID: "disabled", Provider: "codex", Disabled: true, ModelStates: rest("model", later, "limit")},
		{ID: "other-provider", Provider: "claude", ModelStates: rest("model", later, "limit")},
	} {
		if _, err := m.Register(coreauth.WithSkipPersist(context.Background()), a); err != nil {
			t.Fatal(err)
		}
	}
	for id, want := range map[string]string{"resting": `429 {"error":{"message":"usage limit reached"}}`, "account-wide": "429 five-hour limit",
		"rested-before": "503", "other-model": "503", "disabled": "503", "other-provider": "503", "missing": "503"} {
		options := executor.Options{Headers: http.Header{}}
		options.Headers.Set(accountHeader, id)
		options.Headers.Set(providerHeader, "codex")
		_, err := (exactSelector{m}).Pick(context.Background(), "codex", "model", options, []*coreauth.Auth{{ID: "fallback", Provider: "codex"}})
		var refusal *coreauth.Error
		if !errors.As(err, &refusal) {
			t.Fatalf("%s: %v", id, err)
		}
		got := fmt.Sprint(refusal.HTTPStatus)
		if refusal.HTTPStatus != http.StatusServiceUnavailable {
			// No code of its own, so that the session reads the provider's words alone.
			got += " " + refusal.Code + refusal.Message
		}
		if got != want {
			t.Fatalf("%s: %s, want %s", id, got, want)
		}
	}
}

func TestUnavailableAccountNeverFallsBackToAnotherAccount(t *testing.T) {
	skip := coreauth.WithSkipPersist(context.Background())
	for name, breakIt := range map[string]func(f *fixture) string{
		"account not on the helper": func(*fixture) string { return "codex-ticket-gone" },
		"unknown ticket":            func(*fixture) string { return "unknown" },
		"disabled": func(f *fixture) string {
			_, _ = f.manager.Update(skip, &coreauth.Auth{ID: "codex-a", Provider: "codex", Status: coreauth.StatusActive, Disabled: true})
			return "codex-ticket-a"
		},
		"disabled status": func(f *fixture) string {
			_, _ = f.manager.Update(skip, &coreauth.Auth{ID: "codex-a", Provider: "codex", Status: coreauth.StatusDisabled})
			return "codex-ticket-a"
		},
		"removed": func(f *fixture) string {
			f.manager.Remove(skip, "codex-a")
			return "codex-ticket-a"
		},
		"model not offered": func(*fixture) string {
			cliproxy.GlobalModelRegistry().UnregisterClient("codex-a")
			return "codex-ticket-a"
		},
	} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s stream=%t", name, stream), func(t *testing.T) {
				codex := &fakeProvider{}
				f := twoProviders(t, codex, &fakeProvider{})
				ticket := breakIt(f)
				for attempt := 0; attempt < 3; attempt++ {
					if response := f.model("/v1/responses", ticket, stream); response.Code == http.StatusOK {
						t.Fatalf("an unavailable account answered: %s", response.Body.String())
					}
				}
				if calls := codex.ran(); len(calls) != 0 {
					t.Fatalf("another account ran the request: %v", calls)
				}
				// The other session is untouched, and the broken one keeps its pin.
				if response := f.model("/v1/responses", "codex-ticket-b", stream); response.Code != http.StatusOK || !strings.HasPrefix(codex.ran()[0], "codex-b ") {
					t.Fatalf("the other session broke: %d %v", response.Code, codex.ran())
				}
				if ticket == "codex-ticket-a" && admitted(f.routes, ticket) != "codex-a" {
					t.Fatal("a failed request rewrote the session's account")
				}
			})
		}
	}
}

func TestSwitchingASessionsAccountKeepsItsTicket(t *testing.T) {
	anthropic := &fakeProvider{}
	f := twoProviders(t, &fakeProvider{}, anthropic)
	steps := []struct {
		account string
		status  int
	}{{"claude-a", http.StatusOK}, {"claude-b", http.StatusOK}, {"", http.StatusForbidden}, {"claude-a", http.StatusOK}}
	for _, step := range steps {
		if err := f.routes.Apply([]Route{route("claude-ticket-a", "claude", step.account)}, []string{"claude-a", "claude-b"}); err != nil {
			t.Fatal(err)
		}
		if response := f.model("/v1/messages", "claude-ticket-a", false); response.Code != step.status {
			t.Fatalf("account %q: status=%d body=%s", step.account, response.Code, response.Body.String())
		}
	}
	if want := []string{"claude-a plain", "claude-b plain", "claude-a plain"}; !reflect.DeepEqual(anthropic.ran(), want) {
		t.Fatalf("ran on %v, want %v", anthropic.ran(), want)
	}
}

// A session whose account is signed out or removed hears so once, in its provider's own format and with
// a status its CLI does not retry; a ticket the helper does not know is still unauthorized.
func TestASessionWaitingForASignInIsToldWhyAtOnce(t *testing.T) {
	f := twoProviders(t, &fakeProvider{}, &fakeProvider{})
	if err := f.routes.Apply([]Route{route("claude-ticket-a", "claude", ""), route("codex-ticket-a", "codex", "")}, []string{"claude-a", "codex-a"}); err != nil {
		t.Fatal(err)
	}
	for _, sent := range []struct {
		path, ticket string
		status       int
		kind         string
	}{
		{"/v1/messages", "claude-ticket-a", http.StatusForbidden, "permission_error"},
		{"/v1/responses", "codex-ticket-a", http.StatusBadRequest, "invalid_request_error"},
		{"/v1/messages", "unknown-ticket", http.StatusUnauthorized, "authentication_error"},
	} {
		response := f.model(sent.path, sent.ticket, false)
		var answer struct {
			Type  string
			Error struct{ Type, Message, Code string }
		}
		_ = json.Unmarshal(response.Body.Bytes(), &answer)
		if response.Code != sent.status || answer.Error.Type != sent.kind {
			t.Fatalf("%s: status=%d body=%s", sent.ticket, response.Code, response.Body.String())
		}
		if waiting := sent.status != http.StatusUnauthorized; waiting && (!strings.Contains(answer.Error.Message, "Settings → Accounts") || strings.Contains(response.Body.String(), `\u00`)) {
			t.Fatalf("%s says %s", sent.ticket, response.Body.String())
		}
	}
}

// heldServer serves a fixture whose Codex requests wait until the test lets them go.
func heldServer(t *testing.T) (*httptest.Server, *fixture, *fakeProvider) {
	t.Helper()
	codex := &fakeProvider{started: make(chan string, 8), release: make(chan struct{})}
	f := twoProviders(t, codex, &fakeProvider{})
	server := httptest.NewServer(f.engine)
	t.Cleanup(server.Close)
	return server, f, codex
}

type heldAnswerTo struct {
	status int
	body   string
	err    error
}

func askHeld(server *httptest.Server, ticket string, stream bool) (<-chan heldAnswerTo, context.CancelFunc) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	answer := make(chan heldAnswerTo, 1)
	go func() {
		request, _ := http.NewRequestWithContext(ctx, http.MethodPost, server.URL+"/v1/responses", strings.NewReader(fmt.Sprintf(`{"model":%q,"input":"hi","stream":%t}`, testModel, stream)))
		request.Header.Set("Authorization", "Bearer "+ticket)
		request.Header.Set("Content-Type", "application/json")
		response, err := server.Client().Do(request)
		if err != nil {
			answer <- heldAnswerTo{err: err}
			return
		}
		defer func() { _ = response.Body.Close() }()
		body, err := io.ReadAll(response.Body)
		answer <- heldAnswerTo{status: response.StatusCode, body: string(body), err: err}
	}()
	return answer, cancel
}
func awaitStart(t *testing.T, provider *fakeProvider, want string) {
	t.Helper()
	select {
	case got := <-provider.started:
		if got != want {
			t.Fatalf("the request started on %s, want %s", got, want)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the request was not admitted")
	}
}

// pushUntilIdle retries a push the helper refuses while a finished request is
// still releasing its account.
func pushUntilIdle(t *testing.T, f *fixture, body string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		response := f.send(http.MethodPut, "/ao/routes", control, body)
		if response.Code == http.StatusNoContent {
			return
		}
		if response.Code != http.StatusConflict || time.Now().After(deadline) {
			t.Fatalf("the push was not accepted once idle: %d %s", response.Code, response.Body.String())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestRequestInFlightKeepsItsAccountAndBlocksOnlyDroppingIt(t *testing.T) {
	moved := fmt.Sprintf(`{"routes":[{"ticket_hash":%q,"provider":"codex","auth_id":"codex-b"}],"auth_ids":["codex-a","codex-b"]}`, TicketHash("codex-ticket-a"))
	dropped := fmt.Sprintf(`{"routes":[{"ticket_hash":%q,"provider":"codex","auth_id":"codex-b"}],"auth_ids":["codex-b"]}`, TicketHash("codex-ticket-a"))
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			server, f, codex := heldServer(t)
			first, cancel := askHeld(server, "codex-ticket-a", stream)
			defer cancel()
			awaitStart(t, codex, "codex-a")
			if response := f.send(http.MethodPut, "/ao/routes", control, dropped); response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"SESSION_BUSY"`) {
				t.Fatalf("dropping a busy account: %d %s", response.Code, response.Body.String())
			}
			if admitted(f.routes, "codex-ticket-b") != "codex-b" {
				t.Fatal("a refused push changed the table")
			}
			if response := f.send(http.MethodPut, "/ao/routes", control, moved); response.Code != http.StatusNoContent || response.Body.Len() != 0 {
				t.Fatalf("moving a busy session: %d %s", response.Code, response.Body.String())
			}
			// The moved session's next request runs on its new account at once.
			second, cancelSecond := askHeld(server, "codex-ticket-a", stream)
			defer cancelSecond()
			awaitStart(t, codex, "codex-b")
			close(codex.release)
			for name, answer := range map[string]<-chan heldAnswerTo{"codex-a": first, "codex-b": second} {
				select {
				case got := <-answer:
					if got.err != nil || got.status != http.StatusOK || !strings.Contains(got.body, name) {
						t.Fatalf("the request on %s answered %+v", name, got)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("a request did not finish")
				}
			}
			pushUntilIdle(t, f, dropped)
			if got := codex.ran(); len(got) != 2 || !strings.HasPrefix(got[0], "codex-a ") || !strings.HasPrefix(got[1], "codex-b ") {
				t.Fatalf("a request in flight was rerouted: %v", got)
			}
		})
	}
}

func TestCancelledRequestReleasesItsAccount(t *testing.T) {
	server, f, codex := heldServer(t)
	answer, cancel := askHeld(server, "codex-ticket-a", true)
	awaitStart(t, codex, "codex-a")
	cancel()
	select {
	case got := <-answer:
		if got.err == nil {
			t.Fatalf("a cancelled request finished normally: %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the cancel did not end the request")
	}
	pushUntilIdle(t, f, `{"routes":[],"auth_ids":[]}`)
	close(codex.release)
}

func TestSelectorPicksOnlyTheNamedAccount(t *testing.T) {
	pick := func(dispatch, provider, id string, metadata map[string]any, candidates ...*coreauth.Auth) (*coreauth.Auth, error) {
		options := executor.Options{Headers: http.Header{}, Metadata: metadata}
		options.Headers.Set(accountHeader, id)
		options.Headers.Set(providerHeader, provider)
		return (exactSelector{}).Pick(context.Background(), dispatch, "model", options, candidates)
	}
	for _, provider := range []string{"codex", "claude"} {
		for _, dispatch := range []string{provider, "mixed"} {
			metadata := map[string]any{}
			got, err := pick(dispatch, provider, "wanted", metadata, &coreauth.Auth{ID: "other", Provider: provider}, &coreauth.Auth{ID: "wanted", Provider: provider})
			if err != nil || got.ID != "wanted" || metadata[executor.PinnedAuthMetadataKey] != "wanted" {
				t.Fatalf("dispatch=%s picked %+v, %v, pin %v", dispatch, got, err, metadata)
			}
		}
	}
	if got, err := pick("codex", "codex", "wanted", nil, &coreauth.Auth{ID: "wanted", Provider: "codex"}); err != nil || got == nil {
		t.Fatalf("a request without metadata was refused: %v", err)
	}
	for name, tc := range map[string]struct {
		dispatch, provider, id string
		candidate              coreauth.Auth
	}{
		"no account named":   {"codex", "codex", "", coreauth.Auth{ID: "alice", Provider: "codex"}},
		"unknown account":    {"codex", "codex", "missing", coreauth.Auth{ID: "alice", Provider: "codex"}},
		"other dispatch":     {"claude", "codex", "alice", coreauth.Auth{ID: "alice", Provider: "codex"}},
		"other provider":     {"mixed", "codex", "alice", coreauth.Auth{ID: "alice", Provider: "claude"}},
		"disabled":           {"codex", "codex", "alice", coreauth.Auth{ID: "alice", Provider: "codex", Disabled: true}},
		"disabled by status": {"codex", "codex", "alice", coreauth.Auth{ID: "alice", Provider: "codex", Status: coreauth.StatusDisabled}},
	} {
		got, err := pick(tc.dispatch, tc.provider, tc.id, map[string]any{}, &tc.candidate, &coreauth.Auth{ID: "fallback", Provider: "codex"})
		if err == nil || got != nil {
			t.Fatalf("%s: picked %+v", name, got)
		}
	}
}
