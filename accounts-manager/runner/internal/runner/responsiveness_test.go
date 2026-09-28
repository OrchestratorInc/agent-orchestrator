//go:build performance

package runner

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const responsivenessAccountCount = 50
const responsivenessBindingCount = 20

type responsivenessRig struct {
	t        *testing.T
	client   *http.Client
	baseURL  string
	upstream *httptest.Server
	stateDir string
	keys     []string
	refs     []string
	tokens   []string
	bindings routeBindingSnapshot
	model    string
	logs     *lockedBuffer
	command  *exec.Cmd
	requests atomic.Int64
	verified [responsivenessAccountCount]atomic.Int32
}

func newResponsivenessRig(t *testing.T) *responsivenessRig {
	t.Helper()
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	rig := &responsivenessRig{
		t: t, client: &http.Client{Timeout: 5 * time.Second, Transport: transport},
		keys: make([]string, responsivenessAccountCount), refs: make([]string, responsivenessAccountCount),
		tokens: make([]string, responsivenessBindingCount), logs: &lockedBuffer{},
		bindings: routeBindingSnapshot{Revision: 1},
	}
	rig.upstream = httptest.NewServer(http.HandlerFunc(rig.handleUpstream))
	t.Cleanup(rig.upstream.Close)
	t.Cleanup(transport.CloseIdleConnections)
	t.Cleanup(rig.stop)
	rig.stateDir, _ = validStateFixture(t)
	port := reservePort(t)
	rig.baseURL = "http://127.0.0.1:" + strconv.Itoa(port)
	configPath := filepath.Join(rig.stateDir, configFileName)
	replaceInFile(t, configPath, "43127", strconv.Itoa(port))
	config, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatal(err)
	}
	var appended strings.Builder
	appended.WriteString("codex-api-key:\n")
	for i := range responsivenessAccountCount {
		rig.keys[i] = fmt.Sprintf("synthetic-measurement-key-%03d", i)
		auth := &coreauth.Auth{Provider: "codex", Attributes: map[string]string{"api_key": rig.keys[i], "base_url": rig.upstream.URL + "/v1"}}
		rig.refs[i] = auth.EnsureIndex()
		fmt.Fprintf(&appended, "  - api-key: %s\n    base-url: %s/v1\n", rig.keys[i], rig.upstream.URL)
		if i < responsivenessBindingCount {
			rig.bindings.Bindings = append(rig.bindings.Bindings, routeBinding{
				SessionID: fmt.Sprintf("measurement-%03d", i), Provider: "codex", Mode: "managed",
				AccountID: publicCredentialID("management-secret", rig.refs[i]), Revision: 1,
			})
		}
	}
	writePrivateFile(t, configPath, string(config)+appended.String())
	catalog, err := loadCredentialModels()
	if err != nil || len(catalog["codex"]) == 0 {
		t.Fatal("measurement model catalog unavailable")
	}
	rig.model = catalog["codex"][0].ID
	return rig
}

func (r *responsivenessRig) handleUpstream(w http.ResponseWriter, request *http.Request) {
	if request.URL.Path == "/v1/responses" {
		r.requests.Add(1)
	}
	index := -1
	for i, key := range r.keys {
		if request.Header.Get("Authorization") == "Bearer "+key {
			index = i
			break
		}
	}
	if index < 0 {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/models" {
		r.verified[index].Add(1)
		_, _ = io.WriteString(w, `{"data":[{"id":"test-model"}]}`)
		return
	}
	body, err := io.ReadAll(io.LimitReader(request.Body, 1<<20))
	marker, _ := json.Marshal(fmt.Sprintf("measurement-account-%03d", index))
	if err != nil || request.Method != http.MethodPost || request.URL.Path != "/v1/responses" || !bytes.Contains(body, marker) {
		w.WriteHeader(http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	_, _ = io.WriteString(w, "data: {\"type\":\"response.output_text.delta\",\"delta\":\"selected\"}\n\n")
	w.(http.Flusher).Flush()
	_, _ = io.WriteString(w, "data: {\"type\":\"response.completed\",\"response\":{\"id\":\"measurement\",\"status\":\"completed\",\"output\":[]}}\n\n")
	w.(http.Flusher).Flush()
}

func (r *responsivenessRig) stop() {
	if r.command != nil {
		_ = r.command.Process.Kill()
		_ = r.command.Wait()
		r.command = nil
	}
}

func (r *responsivenessRig) start() {
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
			r.t.Fatal("measurement startup model registry did not settle")
		}
		time.Sleep(time.Millisecond)
	}
	r.inventory()
}

func (r *responsivenessRig) request(method, target, token string, body any) *http.Response {
	r.t.Helper()
	var encoded []byte
	if body != nil {
		var err error
		encoded, err = json.Marshal(body)
		if err != nil {
			r.t.Fatal(err)
		}
	}
	request, err := http.NewRequestWithContext(r.t.Context(), method, target, bytes.NewReader(encoded))
	if err != nil {
		r.t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/json")
	response, err := r.client.Do(request)
	if err != nil {
		r.t.Fatal(err)
	}
	return response
}

func (r *responsivenessRig) control(method, path string, body any, status int) []byte {
	r.t.Helper()
	response := r.request(method, r.baseURL+path, "management-secret", body)
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil || response.StatusCode != status {
		r.t.Fatalf("measurement control method=%s status=%d want=%d readError=%t", method, response.StatusCode, status, err != nil)
	}
	return raw
}

func (r *responsivenessRig) inventory() {
	r.t.Helper()
	raw := r.control(http.MethodGet, credentialPath, nil, http.StatusOK)
	var inventory struct {
		Files []credentialRecord `json:"files"`
	}
	if json.Unmarshal(raw, &inventory) != nil || len(inventory.Files) != responsivenessAccountCount {
		r.t.Fatal("measurement inventory has wrong account count")
	}
	seen := make(map[string]bool, len(inventory.Files))
	for _, record := range inventory.Files {
		seen[record.AuthIndex] = true
	}
	for _, ref := range r.refs {
		if !seen[ref] {
			r.t.Fatal("measurement inventory identity changed")
		}
	}
}

func (r *responsivenessRig) reconcile() {
	r.control(http.MethodPut, "/ao/internal/routes/bindings", r.bindings, http.StatusNoContent)
}

func (r *responsivenessRig) mintBody(binding, account int, revision int64) any {
	return map[string]any{"provider": "codex", "authIndex": r.refs[account],
		"sessionId": r.bindings.Bindings[binding].SessionID, "accountId": publicCredentialID("management-secret", r.refs[account]),
		"bindingRevision": revision}
}

func (r *responsivenessRig) mint(index int) float64 {
	r.t.Helper()
	started := time.Now()
	raw := r.control(http.MethodPost, "/ao/internal/routes/token", r.mintBody(index, index, 1), http.StatusOK)
	var token struct {
		Token string `json:"token"`
	}
	if json.Unmarshal(raw, &token) != nil || token.Token == "" {
		r.t.Fatal("measurement route capability absent")
	}
	r.tokens[index] = token.Token
	return milliseconds(time.Since(started))
}

func (r *responsivenessRig) stream(index int, direct bool) (responseMeasurement, error) {
	started := time.Now()
	target, token := r.baseURL, r.tokens[index]
	if direct {
		target, token = r.upstream.URL, r.keys[index]
	}
	response := r.request(http.MethodPost, target+"/v1/responses", token, map[string]any{
		"model": r.model, "input": fmt.Sprintf("measurement-account-%03d", index), "stream": true,
	})
	defer response.Body.Close()
	return readMeasuredStream(response, started, time.Now)
}

func (r *responsivenessRig) requireStream(index int, direct bool) responseMeasurement {
	r.t.Helper()
	result, err := r.stream(index, direct)
	if err != nil {
		r.t.Fatalf("measurement stream binding=%d direct=%t failed: %v", index, direct, err)
	}
	return result
}

func (r *responsivenessRig) requireBlocked(index int) {
	r.t.Helper()
	before := r.requests.Load()
	response := r.request(http.MethodPost, r.baseURL+"/v1/responses", r.tokens[index], map[string]any{
		"model": r.model, "input": fmt.Sprintf("measurement-account-%03d", index), "stream": true,
	})
	defer response.Body.Close()
	_, err := io.Copy(io.Discard, response.Body)
	if err != nil || response.StatusCode != http.StatusUnauthorized || r.requests.Load() != before {
		r.t.Fatal("unadmitted capability reached the upstream")
	}
}

func TestRunnerResponsiveness(t *testing.T) {
	warmCount, coldCount := 100, 30
	if testing.Short() {
		warmCount, coldCount = 20, 2
	}
	rig := newResponsivenessRig(t)
	firstStart := time.Now()
	rig.start()
	for _, ref := range rig.refs {
		rig.control(http.MethodPost, credentialPath+"/refresh?ref="+ref, nil, http.StatusOK)
	}
	for i := range responsivenessAccountCount {
		if rig.verified[i].Load() == 0 {
			t.Fatalf("synthetic account %d was not verified with the upstream", i)
		}
	}
	rig.reconcile()
	for i := range responsivenessBindingCount {
		rig.mint(i)
	}
	rig.requireStream(0, false)
	firstStartMS := milliseconds(time.Since(firstStart))
	assertVaultHasNoSecret(t, rig.stateDir)
	for i := range responsivenessBindingCount {
		rig.reconcile()
		rig.requireStream(i, true)
		rig.requireStream(i, false)
	}
	preparations := make([]float64, 0, warmCount)
	directFirst, proxyFirst, extraFirst := make([]float64, 0, warmCount), make([]float64, 0, warmCount), make([]float64, 0, warmCount)
	directComplete, proxyComplete := make([]float64, 0, warmCount), make([]float64, 0, warmCount)
	for sample := range warmCount {
		index := sample % responsivenessBindingCount
		rig.reconcile()
		preparations = append(preparations, rig.mint(index))
		var direct, proxy responseMeasurement
		if sample%2 == 0 {
			direct, proxy = rig.requireStream(index, true), rig.requireStream(index, false)
		} else {
			proxy, direct = rig.requireStream(index, false), rig.requireStream(index, true)
		}
		directFirst, proxyFirst = append(directFirst, direct.FirstMS), append(proxyFirst, proxy.FirstMS)
		extraFirst = append(extraFirst, proxy.FirstMS-direct.FirstMS)
		directComplete, proxyComplete = append(directComplete, direct.CompleteMS), append(proxyComplete, proxy.CompleteMS)
	}
	cold := make([]float64, 0, coldCount)
	for sample := range coldCount {
		rig.stop()
		started := time.Now()
		rig.start()
		rig.requireBlocked(0)
		rig.reconcile()
		rig.mint(sample % responsivenessBindingCount)
		rig.requireStream(sample%responsivenessBindingCount, false)
		cold = append(cold, milliseconds(time.Since(started)))
		rig.requireStream((sample+1)%responsivenessBindingCount, false)
	}
	rig.reconcile()
	before := rig.requests.Load()
	rig.control(http.MethodPost, "/ao/internal/routes/token", rig.mintBody(0, 1, 1), http.StatusConflict)
	rig.control(http.MethodPost, "/ao/internal/routes/token", rig.mintBody(0, 0, 2), http.StatusConflict)
	if rig.requests.Load() != before {
		t.Fatal("invalid route preparation reached upstream")
	}
	rig.bindings.Revision = 2
	rig.bindings.Bindings[0].Blocked = true
	rig.reconcile()
	rig.requireBlocked(0)
	rig.requireStream(1, false)
	rig.stop()
	wantRequests := int64(2 + 2*(responsivenessBindingCount+warmCount+coldCount))
	if rig.requests.Load() != wantRequests {
		t.Fatalf("upstream requests=%d want=%d", rig.requests.Load(), wantRequests)
	}
	for _, key := range rig.keys {
		if strings.Contains(rig.logs.String(), key) {
			t.Fatal("measurement child logged a credential")
		}
	}
	for _, token := range rig.tokens {
		if strings.Contains(rig.logs.String(), token) {
			t.Fatal("measurement child logged a capability")
		}
	}
	assertVaultHasNoSecret(t, rig.stateDir)
	metrics := map[string][]float64{
		"capabilityPreparation": preparations, "directFirstEvent": directFirst, "proxyFirstEvent": proxyFirst,
		"pairedAdditionalFirstEvent": extraFirst, "directComplete": directComplete, "proxyComplete": proxyComplete, "runnerRestart": cold,
	}
	distributions := make(map[string]measurementDistribution, len(metrics))
	for name, values := range metrics {
		distributions[name] = measuredDistribution(values)
	}
	report := map[string]any{
		"accounts": responsivenessAccountCount, "bindings": responsivenessBindingCount, "failures": 0,
		"upstreamResponseRequests":                 rig.requests.Load(),
		"firstMigrationVerificationAndAdmissionMs": firstStartMS,
		"toolchain": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH,
		"cpus": runtime.NumCPU(), "gomaxprocs": runtime.GOMAXPROCS(0),
		"samplesMs": metrics, "distributions": distributions,
	}
	encoded, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("runner responsiveness metrics: %s", encoded)
	if testing.Short() {
		t.Log("runner responsiveness correctness passed")
	} else {
		t.Log("runner responsiveness measurement complete")
	}
}
