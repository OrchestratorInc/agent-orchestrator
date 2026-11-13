package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type accountServer struct {
	mu       sync.Mutex
	requests []string
	switches []switchSessionAccountRequest
	polls    int
	final    string
}

func (a *accountServer) log(r *http.Request) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.requests = append(a.requests, r.Method+" "+r.URL.RequestURI())
}

func newAccountServer(t *testing.T, final string) (*httptest.Server, *accountServer) {
	t.Helper()
	state := &accountServer{final: final}
	accounts := `{"accounts":[` +
		`{"id":"device-claude","provider":"claude","device":true,"email":"me@example.com","plan":"max","default":true,"signedIn":true,"loggedIn":true},` +
		`{"id":"work","provider":"claude","device":false,"email":"work@example.com","plan":"team","default":false,"signedIn":true,"loggedIn":true},` +
		`{"id":"device-codex","provider":"codex","device":true,"default":true,"signedIn":false,"loggedIn":false}]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		state.log(r)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents/accounts":
			_, _ = io.WriteString(w, accounts)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/agents/accounts/work/default":
			_, _ = io.WriteString(w, accounts)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1":
			_, _ = io.WriteString(w, `{"session":`+sessionJSON("demo-1", "demo", "worker", "idle", false)+`}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/ghost":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, `{"error":"not_found","code":"SESSION_NOT_FOUND","message":"Unknown session"}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/agents/accounts/sessions/demo-1":
			_, _ = io.WriteString(w, `{"claude":"work","codex":"device-codex"}`)
		case r.Method == http.MethodPut && r.URL.Path == "/api/v1/agents/accounts/sessions/demo-1":
			var req switchSessionAccountRequest
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			state.mu.Lock()
			state.switches = append(state.switches, req)
			state.mu.Unlock()
			w.WriteHeader(http.StatusAccepted)
			_, _ = io.WriteString(w, `{"ok":true,"sessionId":"demo-1","transition":{"id":"t-1","phase":"requested","targetAccountId":`+jsonQuote(req.AccountID)+`}}`)
		case r.Method == http.MethodGet && r.URL.Path == "/api/v1/sessions/demo-1/interface-transition":
			state.mu.Lock()
			state.polls++
			phase := "draining"
			if state.polls > 1 {
				phase = state.final
			}
			state.mu.Unlock()
			_, _ = io.WriteString(w, `{"supported":true,"targetMode":"chat","transition":{"id":"t-1","phase":"`+phase+`","errorDetail":"the account is signed out"}}`)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, state
}

func runAccountCLI(t *testing.T, final string, args ...string) (string, *accountServer, error) {
	t.Helper()
	cfg := setConfigEnv(t)
	srv, state := newAccountServer(t, final)
	writeRunFileFor(t, cfg, srv)
	out, errOut, err := executeCLI(t, Deps{ProcessAlive: func(int) bool { return true }}, args...)
	if err != nil {
		t.Logf("stderr: %s", errOut)
	}
	return out, state, err
}

func TestAccountListFiltersByAgent(t *testing.T) {
	out, _, err := runAccountCLI(t, "completed", "account", "ls", "--agent", "claude-code")
	if err != nil {
		t.Fatalf("account ls: %v", err)
	}
	for _, want := range []string{"device-claude", "me@example.com [this computer's login]", "signed in, default", "work@example.com"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "device-codex") {
		t.Fatalf("codex account listed under --agent claude-code:\n%s", out)
	}
}

func TestAccountListRejectsUnknownAgent(t *testing.T) {
	if _, _, err := runAccountCLI(t, "completed", "account", "ls", "--agent", "aider"); err == nil {
		t.Fatal("account ls --agent aider succeeded")
	}
}

func TestAccountDefault(t *testing.T) {
	out, state, err := runAccountCLI(t, "completed", "account", "default", "work")
	if err != nil {
		t.Fatalf("account default: %v", err)
	}
	if !strings.Contains(out, "work (work@example.com) is now the default for new claude-code sessions") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if want := []string{"POST /api/v1/agents/accounts/work/default"}; !reflect.DeepEqual(state.requests, want) {
		t.Fatalf("requests = %#v, want %#v", state.requests, want)
	}
}

func TestAccountShowDefaultsToThisSession(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "demo-1")
	out, _, err := runAccountCLI(t, "completed", "account", "show")
	if err != nil {
		t.Fatalf("account show: %v", err)
	}
	if out != "claude-code: work\ncodex: device-codex\n" {
		t.Fatalf("unexpected output:\n%q", out)
	}
}

func TestAccountShowRefusesUnknownSession(t *testing.T) {
	out, state, err := runAccountCLI(t, "completed", "account", "show", "ghost", "--json")
	if err == nil || !strings.Contains(err.Error(), "Unknown session") {
		t.Fatalf("account show ghost: err = %v, out = %q", err, out)
	}
	if out != "" {
		t.Fatalf("printed accounts for an unknown session: %q", out)
	}
	if want := []string{"GET /api/v1/sessions/ghost"}; !reflect.DeepEqual(state.requests, want) {
		t.Fatalf("requests = %#v, want %#v", state.requests, want)
	}
}

func TestAccountSwitchStartsWithoutWaiting(t *testing.T) {
	out, state, err := runAccountCLI(t, "completed", "account", "switch", "demo-1", "work", "--stop-turn")
	if err != nil {
		t.Fatalf("account switch: %v", err)
	}
	if !strings.Contains(out, "moving demo-1 to work (transition t-1, requested)") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if want := []switchSessionAccountRequest{{AccountID: "work", StopTurn: true}}; !reflect.DeepEqual(state.switches, want) {
		t.Fatalf("switch bodies = %#v, want %#v", state.switches, want)
	}
	if state.polls != 0 {
		t.Fatalf("polled %d times without --wait", state.polls)
	}
}

func TestAccountSwitchWaits(t *testing.T) {
	old := accountSwitchPoll
	accountSwitchPoll = time.Millisecond
	t.Cleanup(func() { accountSwitchPoll = old })

	out, state, err := runAccountCLI(t, "completed", "account", "switch", "demo-1", "work", "--wait")
	if err != nil {
		t.Fatalf("account switch --wait: %v", err)
	}
	if !strings.Contains(out, "moved demo-1 to work") || state.polls != 2 {
		t.Fatalf("output %q after %d polls", out, state.polls)
	}

	_, _, err = runAccountCLI(t, "failed", "account", "switch", "demo-1", "work", "--wait")
	if err == nil || !strings.Contains(err.Error(), "ended failed: the account is signed out") {
		t.Fatalf("failed move error = %v", err)
	}
}
