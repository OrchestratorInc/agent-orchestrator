package host

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/api/handlers"
	coreauth "github.com/router-for-me/CLIProxyAPI/v8/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v8/sdk/config"
)

// providerExecutor answers provider requests by URL and records what was sent.
type providerExecutor struct {
	fakeExecutor
	provider string
	answers  map[string]string
	status   map[string]int
	sending  sync.Mutex
	sent     []string
	bodies   []string
}

func (e *providerExecutor) Identifier() string                               { return e.provider }
func (*providerExecutor) PrepareRequest(*http.Request, *coreauth.Auth) error { return nil }
func (e *providerExecutor) HttpRequest(_ context.Context, _ *coreauth.Auth, r *http.Request) (*http.Response, error) {
	key := r.Method + " " + r.URL.String()
	e.sending.Lock()
	e.sent = append(e.sent, key)
	if r.Body != nil {
		body, _ := io.ReadAll(r.Body)
		e.bodies = append(e.bodies, string(body))
	}
	e.sending.Unlock()
	status := e.status[key]
	if status == 0 {
		status = http.StatusOK
	}
	answer, ok := e.answers[key]
	if !ok {
		status = http.StatusNotFound
	}
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(answer))}, nil
}

func detailsRouter(t *testing.T, e *providerExecutor, attributes map[string]string) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	m := coreauth.NewManager(nil, exactSelector{}, nil)
	m.SetConfig(&config.Config{})
	m.RegisterExecutor(e)
	// A credential read from disk carries the time of its last refresh in its saved fields.
	if _, err := m.Register(context.Background(), &coreauth.Auth{ID: "account", Provider: e.provider, Status: coreauth.StatusActive, Attributes: attributes, Metadata: map[string]any{"last_refresh": "2030-01-01T11:48:00+05:30"}, CreatedAt: time.Date(2029, 9, 3, 8, 0, 0, 0, time.UTC)}); err != nil {
		t.Fatal(err)
	}
	b := Boundary{Routes: testRoutes(t), ControlKey: strings.Repeat("c", 32), InferenceKey: strings.Repeat("i", 32)}
	engine := gin.New()
	engine.Use(b.Middleware)
	b.Configure(engine, handlers.NewBaseAPIHandlers(&config.SDKConfig{}, m), &config.Config{})
	return engine
}

func accountCall(t *testing.T, engine *gin.Engine, path, provider string) (int, map[string]any) {
	t.Helper()
	response := boundaryRequest(engine, "POST", path, strings.Repeat("c", 32), `{"auth_id":"account","provider":"`+provider+`","request_id":"attempt-1"}`, nil)
	var body map[string]any
	_ = json.Unmarshal(response.Body.Bytes(), &body)
	return response.Code, body
}

const (
	claudeOrg         = "0b9d2c5e-3f4a-4b6c-8d7e-9f0a1b2c3d4e"
	claudeClaimCall   = "POST https://api.anthropic.com/api/organizations/" + claudeOrg + "/reset_rate_limits"
	claudeProfileJSON = `{"account": {"email": "alice@example.test", "has_claude_max": true, "has_claude_pro": false}, "organization": {"uuid": "` + claudeOrg + `", "name": "alice@example.test's Organization", "organization_type": "claude_max", "rate_limit_tier": "default_claude_max_20x"}}`
)

func TestAccountDetailsKeepPlanFactsAndDropIdentity(t *testing.T) {
	e := &providerExecutor{provider: "claude", answers: map[string]string{
		"GET " + claudeProfileURL:     claudeProfileJSON,
		"GET " + claudeResetGrantsURL: `{"five_hour": {"utilization": 100}, "cedar_ember": {"eligible": true, "at_limit": true, "grants": [{"id": "g1", "resets_total": 3, "resets_left": 2, "usable_now": true}]}}`,
	}}
	code, body := accountCall(t, detailsRouter(t, e, nil), "/ao/account-details", "claude")
	if code != http.StatusOK {
		t.Fatalf("status %d", code)
	}
	profile, _ := body["profile"].(map[string]any)
	if profile["plan"] != "max" || profile["tier"] != "default_claude_max_20x" {
		t.Fatalf("profile = %v", profile)
	}
	if raw, _ := json.Marshal(body); strings.Contains(string(raw), "alice@example.test") || strings.Contains(string(raw), claudeOrg) {
		t.Fatalf("the profile's identity reached AO: %s", raw)
	}
	grants, _ := body["reset_grants"].(map[string]any)
	if grants["eligible"] != true || body["added_at"] != "2029-09-03T08:00:00Z" || body["refreshed_at"] != "2030-01-01T06:18:00Z" {
		t.Fatalf("details = %v", body)
	}
	if requests, _ := body["requests"].([]any); len(requests) != 20 {
		t.Fatalf("requests = %v", body["requests"])
	}
}

// Codex's profile carries the token tally beside the person's name; only the tally is passed on.
func TestAccountDetailsKeepCodexTokenTallyAndDropIdentity(t *testing.T) {
	e := &providerExecutor{provider: "codex", answers: map[string]string{
		"GET " + codexResetCreditsURL: `{"available_count": 1, "credits": []}`,
		"GET " + codexProfileURL: `{"profile": {"display_name": "Alice Example", "username": "alice.example"}, "metadata": {"stats_as_of": "2030-01-02"}, "stats": {
			"lifetime_tokens": 142300000000, "peak_daily_tokens": 7000000000, "longest_running_turn_sec": 158760, "current_streak_days": 158, "longest_streak_days": 160,
			"daily_usage_buckets": [{"start_date": "2030-01-01", "tokens": 42}, {"start_date": "2030-01-02", "tokens": 1240000}, {"start_date": "2029-12-31", "tokens": 7}],
			"total_threads": 12, "top_invocations": [{"type": "skill", "skill_name": "review"}]
		}}`,
	}}
	code, body := accountCall(t, detailsRouter(t, e, nil), "/ao/account-details", "codex")
	stats, _ := body["token_stats"].(map[string]any)
	if code != http.StatusOK || stats["lifetime_tokens"] != float64(142300000000) || stats["peak_daily_tokens"] != float64(7000000000) || stats["longest_turn_seconds"] != float64(158760) || stats["current_streak_days"] != float64(158) || stats["longest_streak_days"] != float64(160) {
		t.Fatalf("status %d, token stats %v", code, stats)
	}
	// The latest day is the newest one counted, whatever order the provider lists them in.
	if stats["latest_day"] != "2030-01-02" || stats["latest_day_tokens"] != float64(1240000) {
		t.Fatalf("latest day = %v %v", stats["latest_day"], stats["latest_day_tokens"])
	}
	if raw, _ := json.Marshal(body); strings.Contains(string(raw), "Alice") || strings.Contains(string(raw), "alice.example") || strings.Contains(string(raw), "review") {
		t.Fatalf("the profile's identity reached AO: %s", raw)
	}
	// A provider that says nothing about tokens leaves the rest of the details intact.
	e = &providerExecutor{provider: "codex", answers: map[string]string{"GET " + codexResetCreditsURL: `{"available_count": 1}`}}
	if _, body = accountCall(t, detailsRouter(t, e, nil), "/ao/account-details", "codex"); body["token_stats"] != nil || body["reset_credits"] == nil {
		t.Fatalf("details = %v", body)
	}
}

// An API key has no subscription to ask about; only what the helper knows is returned.
func TestAccountDetailsForAnAPIKeyAskTheProviderNothing(t *testing.T) {
	e := &providerExecutor{provider: "codex"}
	code, body := accountCall(t, detailsRouter(t, e, map[string]string{"api_key": "sk-test"}), "/ao/account-details", "codex")
	if code != http.StatusOK || len(e.sent) != 0 || body["added_at"] == nil {
		t.Fatalf("status %d, sent %v, body %v", code, e.sent, body)
	}
}

func TestCodexResetReportsWhatTheProviderDecided(t *testing.T) {
	for answer, want := range map[string]string{
		`{"code": "reset", "windows_reset": 2}`: resetOutcomeReset,
		`{"code": "already_redeemed"}`:          resetOutcomeReset,
		`{"code": "nothing_to_reset"}`:          resetOutcomeNothing,
		`{"code": "no_credit"}`:                 resetOutcomeNone,
		`{"surprise": true}`:                    resetOutcomeUnknown,
	} {
		e := &providerExecutor{provider: "codex", answers: map[string]string{"POST " + codexResetConsumeURL: answer}}
		code, body := accountCall(t, detailsRouter(t, e, nil), "/ao/account-reset", "codex")
		if code != http.StatusOK || body["outcome"] != want {
			t.Fatalf("%s: status %d outcome %v, want %s", answer, code, body["outcome"], want)
		}
		// One attempt, carrying the caller's identity for it.
		if len(e.bodies) != 1 || e.bodies[0] != `{"redeem_request_id":"attempt-1"}` {
			t.Fatalf("%s: sent %v", answer, e.bodies)
		}
	}
	// A failure after sending leaves the outcome unknown; a refusal does not.
	for status, want := range map[int]string{http.StatusBadGateway: resetOutcomeUnknown, http.StatusForbidden: resetOutcomeFailed, http.StatusTooManyRequests: resetOutcomeWait} {
		call := "POST " + codexResetConsumeURL
		e := &providerExecutor{provider: "codex", answers: map[string]string{call: `{}`}, status: map[string]int{call: status}}
		if _, body := accountCall(t, detailsRouter(t, e, nil), "/ao/account-reset", "codex"); body["outcome"] != want {
			t.Fatalf("HTTP %d: outcome %v, want %s", status, body["outcome"], want)
		}
	}
}

func TestClaudeResetClaimsOnlyAGrantTheProviderWouldAccept(t *testing.T) {
	grants := func(status string) map[string]string {
		return map[string]string{"GET " + claudeProfileURL: claudeProfileJSON, "GET " + claudeResetGrantsURL: `{"cedar_ember": ` + status + `}`, claudeClaimCall: `{"result": "reset"}`}
	}
	claimed := func(e *providerExecutor) bool {
		for _, call := range e.sent {
			if call == claudeClaimCall {
				return true
			}
		}
		return false
	}
	t.Run("spends the grant the provider recommends", func(t *testing.T) {
		e := &providerExecutor{provider: "claude", answers: grants(`{"eligible": true, "at_limit": true, "next_grant_id": "g2", "grants": [{"id": "g1", "resets_left": 1, "usable_now": true}, {"id": "g2", "resets_left": 1, "usable_now": true}]}`)}
		if _, body := accountCall(t, detailsRouter(t, e, nil), "/ao/account-reset", "claude"); body["outcome"] != resetOutcomeReset {
			t.Fatalf("outcome %v", body["outcome"])
		}
		if len(e.bodies) != 1 || e.bodies[0] != `{"grant_id":"g2","program":"cedar_ember","request_id":"attempt-1"}` {
			t.Fatalf("claim = %v", e.bodies)
		}
	})
	for name, test := range map[string]struct{ status, want string }{
		"below the limit":   {`{"eligible": true, "at_limit": false, "grants": [{"id": "g1", "resets_left": 1, "usable_now": true}]}`, resetOutcomeNothing},
		"during a cooldown": {`{"eligible": true, "at_limit": true, "cooldown_until": "2099-01-01T00:00:00Z", "grants": [{"id": "g1", "resets_left": 1, "usable_now": true}]}`, resetOutcomeWait},
		"not eligible":      {`{"eligible": false, "grants": [{"id": "g1", "resets_left": 1, "usable_now": true}]}`, resetOutcomeNone},
		"paused grant":      {`{"eligible": true, "at_limit": true, "grants": [{"id": "g1", "resets_left": 1, "usable_now": true, "paused": true}]}`, resetOutcomeNone},
		"nothing left":      {`{"eligible": true, "at_limit": true, "grants": [{"id": "g1", "resets_left": 0, "usable_now": true}]}`, resetOutcomeNone},
	} {
		t.Run(name, func(t *testing.T) {
			e := &providerExecutor{provider: "claude", answers: grants(test.status)}
			if _, body := accountCall(t, detailsRouter(t, e, nil), "/ao/account-reset", "claude"); body["outcome"] != test.want {
				t.Fatalf("outcome %v, want %s", body["outcome"], test.want)
			}
			if claimed(e) {
				t.Fatal("a claim the provider would refuse was sent")
			}
		})
	}
}

func TestAccountResetRefusesAPIKeysAndMalformedAttempts(t *testing.T) {
	e := &providerExecutor{provider: "codex", answers: map[string]string{"POST " + codexResetConsumeURL: `{"code": "reset"}`}}
	if code, _ := accountCall(t, detailsRouter(t, e, map[string]string{"api_key": "sk-test"}), "/ao/account-reset", "codex"); code != http.StatusBadRequest || len(e.sent) != 0 {
		t.Fatalf("API key: status %d, sent %v", code, e.sent)
	}
	engine := detailsRouter(t, e, nil)
	if code := boundaryRequest(engine, "POST", "/ao/account-reset", strings.Repeat("c", 32), `{"auth_id":"account","provider":"codex","request_id":"bad id!"}`, nil).Code; code != http.StatusBadRequest || len(e.sent) != 0 {
		t.Fatalf("malformed attempt: status %d, sent %v", code, e.sent)
	}
	// Without the daemon's key, none of the account routes answer.
	for _, path := range []string{"/ao/account-details", "/ao/account-reset", "/ao/account-resume", "/ao/account-refresh"} {
		if code := boundaryRequest(engine, "POST", path, "wrong", `{"auth_id":"account","provider":"codex"}`, nil).Code; code != http.StatusUnauthorized {
			t.Fatalf("%s without the control key: status %d", path, code)
		}
	}
}

// renewsBefore stands in for a provider's rule of renewing a sign-in this long
// before its token runs out.
type renewsBefore time.Duration

func (r renewsBefore) RefreshLead() *time.Duration { lead := time.Duration(r); return &lead }

func TestSignInEndingIsReportedOnceRenewalIsWellOverdue(t *testing.T) {
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	signIn := func(change func(*coreauth.Auth)) *coreauth.Auth {
		auth := &coreauth.Auth{ID: "a", Provider: "claude", Runtime: renewsBefore(4 * time.Hour), Metadata: map[string]any{}}
		change(auth)
		return auth
	}
	expiresIn := func(left time.Duration) func(*coreauth.Auth) {
		return func(auth *coreauth.Auth) { auth.Metadata["expired"] = now.Add(left).Format(time.RFC3339) }
	}
	for name, test := range map[string]struct {
		auth   *coreauth.Auth
		ending bool
		until  time.Time
	}{
		"not yet due for renewal":         {signIn(expiresIn(6 * time.Hour)), false, time.Time{}},
		"due, and CLIProxy is on it":      {signIn(expiresIn(4*time.Hour - 5*time.Minute)), false, time.Time{}},
		"due half an hour ago, unrenewed": {signIn(expiresIn(3 * time.Hour)), true, now.Add(3 * time.Hour)},
		"refused by the provider": {signIn(func(auth *coreauth.Auth) {
			expiresIn(3 * time.Hour)(auth)
			auth.LastError = &coreauth.Error{Code: "unauthorized", HTTPStatus: http.StatusUnauthorized}
		}), true, now.Add(3 * time.Hour)},
		"the provider could not be reached": {signIn(func(auth *coreauth.Auth) {
			expiresIn(3 * time.Hour)(auth)
			auth.LastError = &coreauth.Error{Message: "dial tcp: no route to host"}
		}), false, time.Time{}},
		"no expiry, renewed long ago": {signIn(func(auth *coreauth.Auth) {
			auth.Metadata["last_refresh"] = now.Add(-5 * time.Hour).Format(time.RFC3339)
		}), true, time.Time{}},
		"no expiry, renewed recently": {signIn(func(auth *coreauth.Auth) {
			auth.Metadata["last_refresh"] = now.Add(-4*time.Hour - 10*time.Minute).Format(time.RFC3339)
		}), false, time.Time{}},
		"an API key never renews": {signIn(func(auth *coreauth.Auth) {
			expiresIn(time.Hour)(auth)
			auth.Attributes = map[string]string{"api_key": "k"}
		}), false, time.Time{}},
		"a provider with no renewal rule": {signIn(func(auth *coreauth.Auth) {
			expiresIn(time.Hour)(auth)
			auth.Provider, auth.Runtime = "unknown-provider", nil
		}), false, time.Time{}},
	} {
		t.Run(name, func(t *testing.T) {
			ending, until := signInEnding(test.auth, now)
			if ending != test.ending || !until.Equal(test.until) {
				t.Fatalf("ending=%v until=%v, want %v %v", ending, until, test.ending, test.until)
			}
		})
	}
}
