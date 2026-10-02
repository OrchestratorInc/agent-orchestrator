package runner

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

type parallelStream struct {
	account   string
	entered   chan struct{}
	forwarded chan struct{}
	release   chan struct{}
	once      sync.Once
	abort     bool
}

type parallelRunner struct {
	t        *testing.T
	client   *http.Client
	upstream *httptest.Server
	baseURL  string
	stateDir string
	model    string
	command  *exec.Cmd
	logs     *lockedBuffer
	keys     map[string]string
	refs     map[string]string
	tokens   map[string]string
	issued   []string
	bindings routeBindingSnapshot
	mu       sync.Mutex
	streams  map[string]*parallelStream
	seen     map[string][]string
}

func newParallelRunner(t *testing.T) *parallelRunner {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	r := &parallelRunner{
		t: t, client: &http.Client{Transport: transport, Timeout: 10 * time.Second},
		logs: &lockedBuffer{}, keys: map[string]string{"a": "synthetic-parallel-vault-secret-a", "b": "synthetic-parallel-vault-secret-b"},
		refs: make(map[string]string), tokens: make(map[string]string),
		streams: make(map[string]*parallelStream), seen: make(map[string][]string),
		bindings: routeBindingSnapshot{Revision: 1},
	}
	r.stateDir, _ = validStateFixture(t)
	r.upstream = httptest.NewServer(http.HandlerFunc(r.handleUpstream))
	t.Cleanup(func() {
		r.upstream.CloseClientConnections()
		r.upstream.Close()
	})
	t.Cleanup(transport.CloseIdleConnections)
	t.Cleanup(func() {
		r.stop()
		for _, secret := range append([]string{r.keys["a"], r.keys["b"], "management-secret", "control-secret"}, r.issued...) {
			if secret != "" && strings.Contains(r.logs.String(), secret) {
				t.Error("runner diagnostics exposed a fixture credential")
			}
		}
		assertVaultHasNoSecret(t, r.stateDir)
	})
	port := reservePort(t)
	r.baseURL = "http://127.0.0.1:" + strconv.Itoa(port)
	replaceInFile(t, filepath.Join(r.stateDir, configFileName), "43127", strconv.Itoa(port))
	catalog, err := loadCredentialModels()
	if err != nil || len(catalog["codex"]) == 0 {
		t.Fatal("test model catalog unavailable")
	}
	r.model = catalog["codex"][0].ID
	r.start()
	for _, name := range []string{"a", "b"} {
		raw := r.control(http.MethodPost, credentialPath+"/api-key", map[string]string{
			"operationId": "parallel-connect-" + name, "provider": "codex", "key": r.keys[name], "baseUrl": r.upstream.URL + "/v1",
		}, http.StatusOK)
		var record credentialRecord
		if json.Unmarshal(raw, &record) != nil || record.AuthIndex == "" || record.Verification != "verified" {
			t.Fatal("credential creation did not return a verified account")
		}
		r.refs[name] = record.AuthIndex
		r.bindings.Bindings = append(r.bindings.Bindings, routeBinding{
			SessionID: "parallel-" + name, Provider: "codex", Mode: "managed",
			AccountID: publicCredentialID("management-secret", record.AuthIndex), Revision: 1,
		})
	}
	r.reconcile()
	for _, name := range []string{"a", "b"} {
		r.tokens[name] = r.mint(name, name, 1)
	}
	return r
}

func (r *parallelRunner) handleUpstream(w http.ResponseWriter, request *http.Request) {
	account := ""
	for name, key := range r.keys {
		if request.Header.Get("Authorization") == "Bearer "+key {
			account = name
			break
		}
	}
	if account == "" {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/models" {
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
		return
	}
	if request.Method != http.MethodPost || request.URL.Path != "/v1/responses" {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	if err != nil {
		w.WriteHeader(http.StatusBadRequest)
		return
	}
	r.mu.Lock()
	var selected *parallelStream
	for marker, stream := range r.streams {
		encoded, _ := json.Marshal(marker)
		if bytes.Contains(body, encoded) {
			selected = stream
			r.seen[marker] = append(r.seen[marker], account)
			break
		}
	}
	r.mu.Unlock()
	if selected == nil || selected.account != account {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = fmt.Fprintf(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"account-%s\"}\n\n", account)
	w.(http.Flusher).Flush()
	selected.once.Do(func() { close(selected.entered) })
	select {
	case <-selected.release:
	case <-request.Context().Done():
		return
	}
	if selected.abort {
		panic(http.ErrAbortHandler)
	}
	_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"parallel-fixture\",\"status\":\"completed\",\"output\":[]}}\n\n")
	w.(http.Flusher).Flush()
}

func (r *parallelRunner) start() {
	r.t.Helper()
	offset := len(r.logs.String())
	r.command = exec.CommandContext(r.t.Context(), os.Args[0], "-test.run=^TestRunnerHelperProcess$")
	r.command.Env = append(os.Environ(), runnerHelperEnvironment+"=1", "AO_ACCOUNTS_MANAGER_TEST_STATE="+r.stateDir,
		"AO_ACCOUNTS_MANAGER_TEST_MODEL_BARRIER=1", "GIN_MODE=release", "GORACE=atexit_sleep_ms=0")
	r.command.Stdout, r.command.Stderr = r.logs, r.logs
	if err := r.command.Start(); err != nil {
		r.command = nil
		r.t.Fatal(err)
	}
	waitForRunnerHealth(r.t, r.baseURL)
	deadline := time.Now().Add(5 * time.Second)
	for !strings.Contains(r.logs.String()[offset:], modelRefreshBarrierMarker) {
		if time.Now().After(deadline) {
			r.t.Fatal("runner model registration did not settle")
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *parallelRunner) stop() {
	if r.command != nil {
		_ = r.command.Process.Kill()
		_ = r.command.Wait()
		r.command = nil
	}
	r.client.CloseIdleConnections()
}

func (r *parallelRunner) request(ctx context.Context, method, path, token string, body any) (*http.Response, error) {
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			return nil, err
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, r.baseURL+path, bytes.NewReader(encoded))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("session_id", "shared-client-hint")
	return r.client.Do(request)
}

func (r *parallelRunner) call(ctx context.Context, method, path, token string, body any) (int, []byte, error) {
	response, err := r.request(ctx, method, path, token, body)
	if err != nil {
		return 0, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	return response.StatusCode, raw, err
}

func (r *parallelRunner) control(method, path string, body any, want int) []byte {
	r.t.Helper()
	status, raw, err := r.call(r.t.Context(), method, path, "management-secret", body)
	if err != nil || status != want {
		r.t.Fatalf("private control method=%s status=%d want=%d transportError=%t", method, status, want, err != nil)
	}
	return raw
}

func (r *parallelRunner) reconcile() {
	r.control(http.MethodPut, "/ao/internal/routes/bindings", r.bindings, http.StatusNoContent)
}

func (r *parallelRunner) mint(session, account string, revision int64) string {
	r.t.Helper()
	raw := r.control(http.MethodPost, "/ao/internal/routes/token", map[string]any{
		"sessionId": "parallel-" + session, "provider": "codex", "authIndex": r.refs[account],
		"accountId": publicCredentialID("management-secret", r.refs[account]), "bindingRevision": revision,
	}, http.StatusOK)
	var route struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &route) != nil || route.Token == "" {
		r.t.Fatal("route capability absent")
	}
	r.issued = append(r.issued, route.Token)
	return route.Token
}

func (r *parallelRunner) prepareStream(marker, account string, held bool) *parallelStream {
	r.t.Helper()
	stream := &parallelStream{account: account, entered: make(chan struct{}), forwarded: make(chan struct{}), release: make(chan struct{})}
	if !held {
		close(stream.release)
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.streams[marker]; exists {
		r.t.Fatal("duplicate request marker")
	}
	r.streams[marker] = stream
	return stream
}

func (r *parallelRunner) stream(ctx context.Context, marker, token, account string) error {
	response, err := r.request(ctx, http.MethodPost, "/v1/responses", token, map[string]any{
		"model": r.model, "input": marker, "stream": true, "prompt_cache_key": "shared-client-hint",
	})
	if err != nil {
		return fmt.Errorf("stream transport failed")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("stream status=%d", response.StatusCode)
	}
	r.mu.Lock()
	stream := r.streams[marker]
	r.mu.Unlock()
	scanner := bufio.NewScanner(io.LimitReader(response.Body, 1<<20))
	sawDelta, sawComplete := false, false
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") || line == "data: [DONE]" {
			continue
		}
		var event struct {
			Type  string `json:"type"`
			Delta string `json:"delta"`
		}
		if json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &event) != nil {
			return fmt.Errorf("malformed stream event")
		}
		switch event.Type {
		case "response.output_text.delta":
			if event.Delta != "account-"+account || sawDelta || stream == nil {
				return fmt.Errorf("stream delivered wrong or repeated identity")
			}
			sawDelta = true
			close(stream.forwarded)
		case "response.completed":
			sawComplete = true
		}
	}
	if scanner.Err() != nil || !sawDelta || !sawComplete {
		return fmt.Errorf("selected response incomplete")
	}
	return nil
}

func (r *parallelRunner) begin(marker, token, account string) <-chan error {
	ctx, cancel := context.WithCancel(r.t.Context())
	done := make(chan error, 1)
	finished := make(chan struct{})
	r.t.Cleanup(func() {
		cancel()
		select {
		case <-finished:
		case <-time.After(5 * time.Second):
			r.t.Error("cancelled stream worker did not exit")
		}
	})
	go func() {
		defer close(finished)
		done <- r.stream(ctx, marker, token, account)
	}()
	return done
}

func (r *parallelRunner) entered(stream *parallelStream, done <-chan error) {
	r.t.Helper()
	for _, stage := range []struct {
		name  string
		ready <-chan struct{}
	}{{"upstream", stream.entered}, {"client", stream.forwarded}} {
		select {
		case <-stage.ready:
		case err := <-done:
			r.t.Fatalf("request finished before %s streaming barrier: %v", stage.name, err)
		case <-time.After(5 * time.Second):
			r.t.Fatalf("request did not reach %s streaming barrier", stage.name)
		}
	}
}

func (r *parallelRunner) completed(done <-chan error) {
	r.t.Helper()
	select {
	case err := <-done:
		if err != nil {
			r.t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		r.t.Fatal("stream did not complete after release")
	}
}

func (r *parallelRunner) assertSeen(marker string, accounts ...string) {
	r.t.Helper()
	r.mu.Lock()
	defer r.mu.Unlock()
	if strings.Join(r.seen[marker], ",") != strings.Join(accounts, ",") {
		r.t.Fatalf("request %s upstream identities=%v want=%v", marker, r.seen[marker], accounts)
	}
}

func TestRunnerParallelSessionsOverlap(t *testing.T) {
	for _, secondAccount := range []string{"b", "a"} {
		t.Run("second_account_"+secondAccount, func(t *testing.T) {
			r := newParallelRunner(t)
			if secondAccount == "a" {
				r.bindings.Revision, r.bindings.Bindings[1].Revision = 2, 2
				r.bindings.Bindings[1].AccountID = r.bindings.Bindings[0].AccountID
				r.reconcile()
				r.tokens["b"] = r.mint("b", "a", 2)
			}
			a := r.prepareStream("overlap-a", "a", true)
			b := r.prepareStream("overlap-b", secondAccount, true)
			doneA := r.begin("overlap-a", r.tokens["a"], "a")
			doneB := r.begin("overlap-b", r.tokens["b"], secondAccount)
			r.entered(a, doneA)
			r.entered(b, doneB)
			for _, done := range []<-chan error{doneA, doneB} {
				select {
				case <-done:
					t.Fatal("stream completed before both responses were released")
				default:
				}
			}
			close(a.release)
			close(b.release)
			r.completed(doneA)
			r.completed(doneB)
			r.assertSeen("overlap-a", "a")
			r.assertSeen("overlap-b", secondAccount)
		})
	}
}

func TestRunnerParallelSessionsMutation(t *testing.T) {
	for _, action := range []string{"fence", "rebind", "native", "disable", "remove"} {
		t.Run(action, func(t *testing.T) {
			r := newParallelRunner(t)
			b := r.prepareStream("held-b", "b", true)
			doneB := r.begin("held-b", r.tokens["b"], "b")
			r.entered(b, doneB)
			switch action {
			case "fence":
				r.bindings.Revision = 2
				r.bindings.Bindings[0].Blocked = true
				r.reconcile()
			case "rebind":
				r.bindings.Revision, r.bindings.Bindings[0].Revision = 2, 2
				r.bindings.Bindings[0].AccountID = r.bindings.Bindings[1].AccountID
				r.reconcile()
			case "native":
				r.bindings.Revision, r.bindings.Bindings[0].Revision = 2, 2
				r.bindings.Bindings[0].Mode, r.bindings.Bindings[0].AccountID = "native", ""
				r.reconcile()
			case "disable":
				r.control(http.MethodPatch, credentialPath+"/status?ref="+url.QueryEscape(r.refs["a"]), map[string]bool{"disabled": true}, http.StatusNoContent)
			case "remove":
				r.control(http.MethodDelete, credentialPath+"?ref="+url.QueryEscape(r.refs["a"]), nil, http.StatusNoContent)
			}
			r.prepareStream("blocked-a", "a", false)
			status, _, err := r.call(t.Context(), http.MethodPost, "/v1/responses", r.tokens["a"], map[string]any{"model": r.model, "input": "blocked-a", "stream": true})
			if err != nil || status != http.StatusUnauthorized {
				t.Fatalf("stale A route status=%d transportError=%t", status, err != nil)
			}
			r.assertSeen("blocked-a")
			r.prepareStream("new-b-during-mutation", "b", false)
			if err := r.stream(t.Context(), "new-b-during-mutation", r.tokens["b"], "b"); err != nil {
				t.Fatal(err)
			}
			r.assertSeen("new-b-during-mutation", "b")
			if action == "rebind" {
				r.prepareStream("rebound-a", "b", false)
				if err := r.stream(t.Context(), "rebound-a", r.mint("a", "b", 2), "b"); err != nil {
					t.Fatal(err)
				}
				r.assertSeen("rebound-a", "b")
			}
			select {
			case err := <-doneB:
				t.Fatalf("A-only mutation ended B stream: %v", err)
			default:
			}
			close(b.release)
			r.completed(doneB)
			r.assertSeen("held-b", "b")
			r.stop()
			r.start()
			status, _, err = r.call(t.Context(), http.MethodGet, "/v1/models", r.tokens["b"], nil)
			if err != nil || status != http.StatusUnauthorized {
				t.Fatalf("unreconciled restart status=%d transportError=%t", status, err != nil)
			}
			r.reconcile()
			status, _, err = r.call(t.Context(), http.MethodGet, "/v1/models", r.tokens["a"], nil)
			if err != nil || status != http.StatusUnauthorized {
				t.Fatalf("restart restored stale A route: status=%d transportError=%t", status, err != nil)
			}
			r.prepareStream("restarted-b", "b", false)
			if err := r.stream(t.Context(), "restarted-b", r.tokens["b"], "b"); err != nil {
				t.Fatal(err)
			}
			r.assertSeen("restarted-b", "b")
		})
	}
}

func TestRunnerParallelSessionsRejectWrongIdentity(t *testing.T) {
	r := newParallelRunner(t)
	r.prepareStream("wrong-identity", "a", false)
	if err := r.stream(t.Context(), "wrong-identity", r.tokens["b"], "a"); err == nil {
		t.Fatal("identity oracle accepted B for an A-only request")
	}
	r.assertSeen("wrong-identity", "b")
}

func TestRunnerParallelSessionsPartialResponseNotReplayed(t *testing.T) {
	r := newParallelRunner(t)
	a := r.prepareStream("interrupted-a", "a", true)
	a.abort = true
	b := r.prepareStream("uninterrupted-b", "b", true)
	doneA := r.begin("interrupted-a", r.tokens["a"], "a")
	doneB := r.begin("uninterrupted-b", r.tokens["b"], "b")
	r.entered(a, doneA)
	r.entered(b, doneB)
	close(a.release)
	select {
	case err := <-doneA:
		if err == nil {
			t.Fatal("interrupted response reported completion")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("interrupted stream did not return")
	}
	r.assertSeen("interrupted-a", "a")
	close(b.release)
	r.completed(doneB)
	r.assertSeen("uninterrupted-b", "b")
}
