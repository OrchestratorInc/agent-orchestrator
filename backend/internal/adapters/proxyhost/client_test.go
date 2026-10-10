package proxyhost

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type transportFunc func(*http.Request) (*http.Response, error)

func (f transportFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func fakeResponse(status int, body string) *http.Response {
	return &http.Response{StatusCode: status, Body: io.NopCloser(strings.NewReader(body)), Header: make(http.Header)}
}

func TestDeleteCredentialUsesNativeAPIKeyIndex(t *testing.T) {
	var requests []*http.Request
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(200, `{"protocol_version":2}`), nil
		}
		requests = append(requests, r)
		if r.Method == http.MethodGet {
			return fakeResponse(200, `{"claude-api-key":[{"api-key":"key","auth-index":"stable-index"}]}`), nil
		}
		return fakeResponse(200, `{}`), nil
	})
	if err := c.DeleteCredential(context.Background(), "config-index:claude:stable-index"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || requests[0].Method != http.MethodGet || requests[1].Method != http.MethodDelete || requests[1].URL.Path != "/v0/management/claude-api-key" || requests[1].URL.Query().Get("index") != "0" {
		t.Fatalf("requests=%v", requests)
	}
}

func TestDeleteCredentialLeavesMissingNativeAPIKeyUntouched(t *testing.T) {
	deletes := 0
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(200, `{"protocol_version":2}`), nil
		}
		if r.Method == http.MethodDelete {
			deletes++
		}
		return fakeResponse(200, `{"codex-api-key":[]}`), nil
	})
	if err := c.DeleteCredential(context.Background(), "config-index:codex:missing"); err != nil {
		t.Fatal(err)
	}
	if deletes != 0 {
		t.Fatalf("unexpected native delete calls=%d", deletes)
	}
}

func TestNativeAPIKeyListFailuresNeverMutateCredentials(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
	}{
		{"unavailable", `{}`, 503},
		{"unauthorized", `{}`, 401},
		{"invalid-json", `{`, 200},
		{"missing-list", `{}`, 200},
		{"wrong-provider", `{"claude-api-key":[]}`, 200},
		{"wrong-list-type", `{"codex-api-key":{}}`, 200},
		{"wrong-entry-type", `{"codex-api-key":["secret"]}`, 200},
	} {
		for _, operation := range []string{"add", "delete"} {
			t.Run(tc.name+"/"+operation, func(t *testing.T) {
				c := privateClient(t, func(r *http.Request) (*http.Response, error) {
					if r.URL.Path == "/ao/status" {
						return fakeResponse(200, `{"protocol_version":2}`), nil
					}
					if r.Method != http.MethodGet || r.URL.Path != "/v0/management/codex-api-key" {
						t.Fatalf("invalid listing triggered %s %s", r.Method, r.URL.Path)
					}
					return fakeResponse(tc.status, tc.body), nil
				})
				var err error
				if operation == "add" {
					_, err = c.StartAccountLoginMode(context.Background(), "codex", "key-1", "api_key", ports.ProviderLoginInput{APIKey: "secret", BaseURL: "https://api.example"})
				} else {
					err = c.DeleteCredential(context.Background(), "config-index:codex:stable")
				}
				if err == nil {
					t.Fatal("invalid native list was accepted")
				}
			})
		}
	}
}

func TestDeleteCredentialUsesCurrentPositionForStableIdentity(t *testing.T) {
	for _, provider := range []string{"codex", "claude"} {
		t.Run(provider, func(t *testing.T) {
			deleted := false
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/ao/status" {
					return fakeResponse(200, `{"protocol_version":2}`), nil
				}
				if r.URL.Path != "/v0/management/"+provider+"-api-key" {
					t.Fatalf("wrong provider path: %s", r.URL.Path)
				}
				if r.Method == http.MethodGet {
					return fakeResponse(200, `{"`+provider+`-api-key":[{"auth-index":123},{"auth-index":"other"},{"auth-index":"stable"}]}`), nil
				}
				if r.Method != http.MethodDelete || r.URL.Query().Get("index") != "2" {
					t.Fatalf("wrong delete: %s %s", r.Method, r.URL)
				}
				deleted = true
				return fakeResponse(200, `{}`), nil
			})
			if err := c.DeleteCredential(context.Background(), "config-index:"+provider+":stable"); err != nil || !deleted {
				t.Fatalf("delete=%v err=%v", deleted, err)
			}
		})
	}
}

func privateClient(t *testing.T, handler transportFunc) *Client {
	t.Helper()
	return &Client{root: t.TempDir(), state: manifest{Port: 12345, ControlKey: strings.Repeat("c", 64), InferenceKey: strings.Repeat("b", 64), TicketKey: strings.Repeat("a", 64)}, http: &http.Client{Transport: handler, Timeout: time.Second}}
}
func writeIdentity(t *testing.T, root string, m manifest) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, "run"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, "run", "host.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
}
func TestProviderHostLoadsStablePrivateIdentity(t *testing.T) {
	root := t.TempDir()
	m := manifest{Port: 32456, PID: 123, ControlKey: strings.Repeat("c", 64), InferenceKey: strings.Repeat("b", 64), TicketKey: strings.Repeat("a", 64)}
	writeIdentity(t, root, m)
	c, err := New(root, "/tmp/ao-proxy-host")
	if err != nil {
		t.Fatal(err)
	}
	if c.state != m || c.Endpoint() != "http://127.0.0.1:32456" {
		t.Fatalf("state=%+v endpoint=%s", c.state, c.Endpoint())
	}
	key, err := c.TicketKey()
	want, _ := hex.DecodeString(m.TicketKey)
	if err != nil || !reflect.DeepEqual(key, want) {
		t.Fatalf("ticket bytes=%x err=%v", key, err)
	}
	again, err := New(root, "different-binary")
	if err != nil || again.state != m {
		t.Fatalf("restart changed identity=%+v err=%v", again, err)
	}
	stat, err := os.Stat(filepath.Join(root, "run", "host.json"))
	if err != nil || stat.Mode().Perm() != 0600 {
		t.Fatalf("identity mode=%v err=%v", stat, err)
	}
}
func TestProviderHostRejectsCorruptOrUnsafeIdentity(t *testing.T) {
	valid := manifest{Port: 32456, ControlKey: strings.Repeat("c", 64), InferenceKey: strings.Repeat("b", 64), TicketKey: strings.Repeat("a", 64)}
	cases := []struct {
		name   string
		change func(*manifest)
	}{
		{"zero-port", func(m *manifest) { m.Port = 0 }},
		{"negative-port", func(m *manifest) { m.Port = -1 }},
		{"overflow-port", func(m *manifest) { m.Port = 65536 }},
		{"short-control-key", func(m *manifest) { m.ControlKey = "c" }},
		{"short-inference-key", func(m *manifest) { m.InferenceKey = "i" }},
		{"short-ticket-key", func(m *manifest) { m.TicketKey = "t" }},
		{"non-hex-control-key", func(m *manifest) { m.ControlKey = strings.Repeat("z", 64) }},
		{"non-hex-inference-key", func(m *manifest) { m.InferenceKey = strings.Repeat("z", 64) }},
		{"non-hex-ticket-key", func(m *manifest) { m.TicketKey = strings.Repeat("z", 64) }},
		{"same-api-keys", func(m *manifest) { m.InferenceKey = m.ControlKey }},
		{"ticket-is-control", func(m *manifest) { m.TicketKey = m.ControlKey }},
		{"ticket-is-inference", func(m *manifest) { m.TicketKey = m.InferenceKey }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			m := valid
			tc.change(&m)
			writeIdentity(t, root, m)
			if _, err := New(root, ""); err == nil {
				t.Fatal("unsafe saved identity accepted")
			}
		})
	}
	t.Run("invalid-json", func(t *testing.T) {
		root := t.TempDir()
		writeIdentity(t, root, valid)
		if err := os.WriteFile(filepath.Join(root, "run", "host.json"), []byte("{"), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := New(root, ""); err == nil {
			t.Fatal("corrupt identity accepted")
		}
	})
	t.Run("relative-data-directory", func(t *testing.T) {
		if _, err := New("relative", ""); err == nil {
			t.Fatal("relative identity accepted")
		}
	})
}
func TestProviderHostHealthyReuseDoesNotSpawnOrChangePID(t *testing.T) {
	calls := 0
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		calls++
		if r.Method != http.MethodGet || r.URL.Path != "/ao/status" || r.URL.Host != "127.0.0.1:12345" {
			t.Fatalf("unsafe probe=%s %s", r.Method, r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer "+strings.Repeat("c", 64) {
			t.Fatal("probe omitted private control key")
		}
		return fakeResponse(200, `{"protocol_version":2,"revision":9,"routes":[]}`), nil
	})
	c.state.PID = 6789
	for i := 0; i < 3; i++ {
		if err := c.Ensure(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	if calls != 3 || c.state.PID != 6789 {
		t.Fatalf("reuse calls=%d pid=%d", calls, c.state.PID)
	}
}
func TestProviderHostIncompatibleProtocolNeverStartsReplacement(t *testing.T) {
	for _, body := range []string{`{"protocol_version":0}`, `{"protocol_version":1}`, `{"protocol_version":3}`, `{"revision":0,"routes":[]}`} {
		t.Run(body, func(t *testing.T) {
			c := privateClient(t, func(*http.Request) (*http.Response, error) { return fakeResponse(200, body), nil })
			c.binary = "/binary-that-must-never-run"
			if err := c.Ensure(context.Background()); !errors.Is(err, errProtocol) {
				t.Fatalf("err=%v", err)
			}
			if c.state.PID != 0 {
				t.Fatal("replacement launched for incompatible running helper")
			}
		})
	}
}
func TestProviderHostUnavailableBuildFailsWithoutAmbientFallback(t *testing.T) {
	c := privateClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("connection refused") })
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "not packaged") {
		t.Fatalf("err=%v", err)
	}
	if c.state.PID != 0 {
		t.Fatal("unavailable build invented a helper pid")
	}
}
func TestProviderHostUnverifiedLiveOwnerIsNeverKilledOrReplaced(t *testing.T) {
	c := privateClient(t, func(*http.Request) (*http.Response, error) { return nil, errors.New("timeout") })
	c.state.PID = os.Getpid()
	c.binary = "/must-never-run"
	if err := c.Ensure(context.Background()); err == nil || !strings.Contains(err.Error(), "ownership is unverified") {
		t.Fatalf("err=%v", err)
	}
	if c.state.PID != os.Getpid() {
		t.Fatal("unverified live process was replaced")
	}
}
func TestProviderHostRouteAcknowledgementMustBeExact(t *testing.T) {
	request := ports.ProviderRouteSnapshot{Revision: 8, Routes: []ports.ProviderRoute{{SessionID: domain.SessionID("s"), Provider: "codex", TicketHash: "hash", AuthID: "exact"}}}
	cases := []struct {
		name, body string
		status     int
		wantErr    bool
	}{
		{"exact", `{"revision":8,"routes":[{"session_id":"s","provider":"codex","ticket_hash":"hash","auth_id":"exact"}]}`, 200, false},
		{"wrong-revision", `{"revision":7,"routes":[{"session_id":"s","provider":"codex","ticket_hash":"hash","auth_id":"exact"}]}`, 200, true},
		{"different-account", `{"revision":8,"routes":[{"session_id":"s","provider":"codex","ticket_hash":"hash","auth_id":"other"}]}`, 200, true},
		{"missing-route", `{"revision":8,"routes":[]}`, 200, true},
		{"invalid-json", `{`, 200, true},
		{"busy", `{"error":"busy"}`, 409, true},
		{"disk-error", `{"error":"disk"}`, 500, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			puts := 0
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/ao/status" {
					return fakeResponse(200, `{"protocol_version":2}`), nil
				}
				puts++
				if r.Method != http.MethodPut || r.URL.Path != "/ao/routes" {
					t.Fatalf("unexpected mutation=%s %s", r.Method, r.URL)
				}
				var sent ports.ProviderRouteSnapshot
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(sent, request) {
					t.Fatalf("route changed in transport=%+v", sent)
				}
				return fakeResponse(tc.status, tc.body), nil
			})
			err := c.ApplyRoutes(context.Background(), request)
			if (err != nil) != tc.wantErr || puts != 1 {
				t.Fatalf("puts=%d err=%v wantErr=%v", puts, err, tc.wantErr)
			}
		})
	}
}

func TestProviderHostFetchAccountUsageUsesNativeCodexProbe(t *testing.T) {
	var sawUsage bool
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(http.StatusOK, `{"protocol_version":2}`), nil
		}
		if r.URL.Path == "/ao/account-usage" {
			sawUsage = true
			var body struct {
				AuthID   string `json:"auth_id"`
				Provider string `json:"provider"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatal(err)
			}
			if body.AuthID != "auth-1" || body.Provider != "codex" {
				t.Fatalf("quota request=%v", body)
			}
			return fakeResponse(http.StatusOK, `{"plan_type":"pro","rate_limit":{"allowed":false,"limit_reached":true,"primary_window":{"used_percent":25,"limit_window_seconds":18000,"reset_at":1893456000},"secondary_window":{"used_percent":100,"limit_window_seconds":604800,"reset_after_seconds":60}},"additional_rate_limits":[{"limit_name":"Spark","rate_limit":{"primary_window":{"used_percent":10,"limit_window_seconds":18000,"reset_at":1893456000}}}],"credits":{"has_credits":true,"unlimited":false,"balance":"12.50"},"rate_limit_reset_credits":{"available_count":2}}`), nil
		}
		if r.URL.Path == "/ao/account-details" {
			return fakeResponse(http.StatusOK, `{"added_at":"2029-09-03T08:00:00Z","requests":[{"time":"11:50-12:00","success":4,"failed":1}],"reset_credits":{"available_count":2,"credits":[{"status":"available","reset_type":"codex_rate_limits","expires_at":"2030-01-21T00:00:00Z"}]}}`), nil
		}
		return fakeResponse(http.StatusNotFound, `{}`), nil
	})
	usage, err := c.FetchAccountUsage(context.Background(), "codex", "auth-1", "private.json")
	if err != nil {
		t.Fatal(err)
	}
	if !sawUsage || usage.Status != "available" || usage.Plan != "pro" || len(usage.Windows) != 3 {
		t.Fatalf("usage=%+v probe=%t", usage, sawUsage)
	}
	if usage.Windows[0].RemainingFraction != .75 || usage.Windows[1].RemainingFraction != 0 {
		t.Fatalf("usage windows were not normalized=%+v", usage.Windows)
	}
	// The general five-hour and weekly limits come first, then the named limit.
	if usage.Windows[0].DurationSeconds != 18000 || usage.Windows[1].DurationSeconds != 604800 || usage.ResetCredits == nil || *usage.ResetCredits != 2 {
		t.Fatalf("usage=%+v", usage)
	}
	if spark := usage.Windows[2]; spark.Name != "Spark" || spark.Scope != domain.ProviderUsageScopeModel || usage.Credits == nil || usage.Credits.Balance != "12.50" {
		t.Fatalf("named limit=%+v credits=%+v", spark, usage.Credits)
	}
	// What the helper knows beyond the usage reading arrives with it.
	if usage.AddedAt != "2029-09-03T08:00:00Z" || len(usage.Requests) != 1 || len(usage.Resets) != 1 || !usage.ResetUsable {
		t.Fatalf("details were not merged: %+v", usage)
	}
}

func TestProviderHostFetchClaudeUsageUsesNativeProbe(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(http.StatusOK, `{"protocol_version":2}`), nil
		}
		// An older helper has no details route; the usage reading stands alone.
		if r.URL.Path != "/ao/account-usage" {
			return fakeResponse(http.StatusNotFound, `{}`), nil
		}
		return fakeResponse(http.StatusOK, `{"five_hour":{"utilization":25,"resets_at":"2030-01-01T00:00:00Z"},"seven_day":{"utilization":90,"resets_at":"2030-01-02T00:00:00Z"},"seven_day_opus":{"utilization":40,"resets_at":"2030-01-02T00:00:00Z"},"seven_day_sonnet":null}`), nil
	})
	usage, err := c.FetchAccountUsage(context.Background(), "claude", "auth-1", "private.json")
	if err != nil || usage.Status != "available" || len(usage.Windows) != 3 || usage.Windows[0].RemainingFraction != .75 || usage.Windows[2].Name != "Opus" {
		t.Fatalf("usage=%+v err=%v", usage, err)
	}
	if usage.Windows[0].DurationSeconds != 18000 || usage.Windows[1].DurationSeconds != 604800 || usage.ResetCredits != nil {
		t.Fatalf("claude usage windows=%+v", usage.Windows)
	}
}

func TestProviderHostFetchAccountModelsKeepsReasoningLevels(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(http.StatusOK, `{"protocol_version":2}`), nil
		}
		if r.URL.Path != "/ao/account-models" {
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL)
		}
		return fakeResponse(http.StatusOK, `{"provider":"codex","models":[{"id":"gpt-5.5","label":"GPT-5.5","is_default":true,"efforts":["low"," medium ","","high"]},{"id":"plain"}]}`), nil
	})
	catalog, err := c.FetchAccountModels(context.Background(), "codex", "auth-1")
	if err != nil || len(catalog.Models) != 2 {
		t.Fatalf("catalog=%+v err=%v", catalog, err)
	}
	// The effort choice is offered from these; a model that reports none offers none.
	if got := strings.Join(catalog.Models[0].Efforts, ","); got != "low,medium,high" {
		t.Fatalf("efforts=%q", got)
	}
	if len(catalog.Models[1].Efforts) != 0 {
		t.Fatalf("plain model efforts=%v", catalog.Models[1].Efforts)
	}
	// An older helper still calls its first entry the default. AO leaves a
	// default model for the agent to choose, so honouring that would run the
	// agent's own choice whenever the first entry was picked.
	if catalog.Models[0].IsDefault || catalog.Models[1].IsDefault {
		t.Fatalf("a model was marked default: %+v", catalog.Models)
	}
}

func TestProviderHostFetchAccountModelsOffersOnlyChatModels(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(http.StatusOK, `{"protocol_version":2}`), nil
		}
		return fakeResponse(http.StatusOK, `{"provider":"codex","models":[{"id":"gpt-5.5"},{"id":"codex-auto-review"},{"id":"gpt-image-2"},{"id":"gpt-image-2.5-flare"},{"id":"gpt-6-sol"}]}`), nil
	})
	catalog, err := c.FetchAccountModels(context.Background(), "codex", "auth-1")
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, model := range catalog.Models {
		ids = append(ids, model.ID)
	}
	if got := strings.Join(ids, ","); got != "gpt-5.5,gpt-6-sol" {
		t.Fatalf("offered %q", got)
	}
}

func TestProviderHostListsOnlyTheSignInsHeldAsFiles(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(http.StatusOK, `{"protocol_version":2}`), nil
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v8/management/credentials" {
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL)
		}
		return fakeResponse(http.StatusOK, `{"files":[
			{"name":"ao-one.json","provider":"codex","source":"file","modtime":"2026-10-03T14:22:01Z"},
			{"name":"key-entry","provider":"claude","source":"memory","modtime":"2026-10-03T14:22:01Z"},
			{"name":"","provider":"codex","source":"file"},
			{"name":"ao-two.json","provider":"claude","source":"file"}
		]}`), nil
	})
	held, err := c.ListCredentials(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// An API key lives in CLIProxy's configuration, not in a file AO may delete.
	if len(held) != 2 || held[0].Name != "ao-one.json" || held[0].Provider != "codex" || held[1].Name != "ao-two.json" {
		t.Fatalf("held=%+v", held)
	}
	if want := time.Date(2026, 10, 3, 14, 22, 1, 0, time.UTC); !held[0].ModifiedAt.Equal(want) || !held[1].ModifiedAt.IsZero() {
		t.Fatalf("times=%v %v", held[0].ModifiedAt, held[1].ModifiedAt)
	}
}

func TestProviderHostReadsSignInFailuresFromCLIProxyState(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(http.StatusOK, `{"protocol_version":2}`), nil
		}
		if r.Method != http.MethodGet || r.URL.Path != "/v8/management/credentials" {
			t.Fatalf("unexpected request=%s %s", r.Method, r.URL)
		}
		return fakeResponse(http.StatusOK, `{"files":[
			{"id":"healthy","status":"active","status_message":""},
			{"id":"cooling","status":"error","status_message":"quota exhausted","unavailable":true},
			{"id":"revoked","status":"disabled","status_message":"disabled (invalid grant)"},
			{"id":"rejected","status":"error","status_message":"unauthorized"},
			{"id":"retrying","status":"error","status_message":"invalid grant (retrying)"},
			{"id":"switched-off","status":"active","disabled":true}]}`), nil
	})
	failures, err := c.AccountSignInFailures(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"revoked", "rejected", "retrying", "switched-off"} {
		if _, failed := failures[id]; !failed {
			t.Fatalf("dead sign-in %q was not reported: %v", id, failures)
		}
	}
	if len(failures) != 4 {
		t.Fatalf("a usable or rate-limited account was reported as signed out: %v", failures)
	}
}

func TestProviderHostFetchAccountUsageRejectsUnsupportedProvider(t *testing.T) {
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		t.Fatal("unsupported provider should not contact helper")
		return nil, nil
	})
	if _, err := c.FetchAccountUsage(context.Background(), "gemini", "auth-1", "private.json"); err == nil {
		t.Fatal("unsupported provider accepted")
	}
}

func TestProviderHostFetchAccountUsageRequiresSignedInIdentity(t *testing.T) {
	c := privateClient(t, func(*http.Request) (*http.Response, error) {
		t.Fatal("unsigned account should not contact helper")
		return nil, nil
	})
	if _, err := c.FetchAccountUsage(context.Background(), "codex", "", "private.json"); err == nil {
		t.Fatal("missing auth identity accepted")
	}
}
func TestProviderHostDeletingCredentialEscapesExactlyOneName(t *testing.T) {
	name := "alice+work?all=true&name=other.json"
	deletes := 0
	c := privateClient(t, func(r *http.Request) (*http.Response, error) {
		if r.URL.Path == "/ao/status" {
			return fakeResponse(200, `{"protocol_version":2}`), nil
		}
		deletes++
		if r.Method != http.MethodDelete || r.URL.Query().Get("name") != name || len(r.URL.Query()) != 1 {
			t.Fatalf("credential query injection=%s", r.URL)
		}
		return fakeResponse(200, `{"status":"ok"}`), nil
	})
	if err := c.DeleteCredential(context.Background(), name); err != nil || deletes != 1 {
		t.Fatalf("delete count=%d err=%v", deletes, err)
	}
}
func TestProviderHostDeleteReplayRequiresVerifiedAbsence(t *testing.T) {
	cases := []struct {
		name, listing string
		listStatus    int
		wantErr       bool
	}{
		{"already-absent", `{"files":[]}`, 200, false},
		{"still-present", `{"files":[{"name":"a.json"}]}`, 200, true},
		{"other-credential", `{"files":[{"name":"b.json"}]}`, 200, false},
		{"missing-files-field", `{}`, 200, true},
		{"malformed-list", `{`, 200, true},
		{"unsupported-list", `{}`, 404, true},
		{"unavailable-list", `{}`, 503, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/ao/status" {
					return fakeResponse(200, `{"protocol_version":2}`), nil
				}
				if r.Method == http.MethodDelete {
					return fakeResponse(404, `{"error":"not found"}`), nil
				}
				if r.Method != http.MethodGet || r.URL.Query().Get("name") != "a.json" {
					t.Fatalf("verification=%s %s", r.Method, r.URL)
				}
				return fakeResponse(tc.listStatus, tc.listing), nil
			})
			err := c.DeleteCredential(context.Background(), "a.json")
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
func TestProviderHostRawErrorNeverExposesProviderResponse(t *testing.T) {
	c := privateClient(t, func(*http.Request) (*http.Response, error) {
		return fakeResponse(500, `{"refresh_token":"PRIVATE-REFRESH","access_token":"PRIVATE-ACCESS"}`), nil
	})
	err := c.call(context.Background(), http.MethodGet, "/ao/status", nil, nil, nil)
	if err == nil || strings.Contains(err.Error(), "PRIVATE") || !strings.Contains(err.Error(), "500") {
		t.Fatalf("unsafe error=%v", err)
	}
}

func TestProviderHostMissingIdentityDoesNotReplaceEstablishedHelper(t *testing.T) {
	for _, name := range []string{"run/routes.json", "config.yaml", "auth/account.json"} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, name)
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				t.Fatal(err)
			}
			original := []byte("existing private helper state")
			if err := os.WriteFile(path, original, 0600); err != nil {
				t.Fatal(err)
			}
			client, err := New(root, "/binary-that-must-never-run")
			if err == nil || client != nil {
				t.Fatal("missing established identity generated a new endpoint and ticket key")
			}
			if !strings.Contains(err.Error(), "identity") {
				t.Fatalf("missing identity was not explained: %v", err)
			}
			if _, err := os.Stat(filepath.Join(root, "run", "host.json")); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("replacement identity was persisted")
			}
			after, err := os.ReadFile(path)
			if err != nil || string(after) != string(original) {
				t.Fatal("existing helper state was modified")
			}
		})
	}
}

func TestRequestBoundaryAcknowledgementIncludesExactAccountInventory(t *testing.T) {
	request := ports.ProviderRouteSnapshot{Revision: 9, AuthIDs: []string{"a", "b"}, RequestBoundary: true,
		Routes: []ports.ProviderRoute{{SessionID: "s", Provider: "codex", TicketHash: "hash", AuthID: "b"}}}
	for _, tc := range []struct {
		name    string
		ids     []string
		wantErr bool
	}{{"exact", []string{"a", "b"}, false}, {"missing", nil, true}, {"retired-active-account", []string{"b"}, true}, {"different-account", []string{"c", "b"}, true}} {
		t.Run(tc.name, func(t *testing.T) {
			c := privateClient(t, func(r *http.Request) (*http.Response, error) {
				if r.URL.Path == "/ao/status" {
					return fakeResponse(200, `{"protocol_version":2}`), nil
				}
				var sent ports.ProviderRouteSnapshot
				if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(sent, request) {
					t.Fatal("transport lost routing instruction or inventory")
				}
				ack := sent
				ack.RequestBoundary = false
				ack.AuthIDs = tc.ids
				body, err := json.Marshal(ack)
				if err != nil {
					t.Fatal(err)
				}
				return fakeResponse(200, string(body)), nil
			})
			if err := c.ApplyRoutes(context.Background(), request); (err != nil) != tc.wantErr {
				t.Fatalf("ack error=%v wantErr=%v", err, tc.wantErr)
			}
		})
	}
}
