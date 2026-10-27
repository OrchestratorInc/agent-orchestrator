package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/hosttunnel"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/go-chi/chi/v5"
)

type remoteHostFakeStore struct {
	Store
	hosts map[string]domain.RemoteHost
	// filledMeanwhile refuses a new host as a full account would after a
	// concurrent registration.
	filledMeanwhile bool
}

func (s *remoteHostFakeStore) ListRemoteHosts(_ context.Context, principal domain.Principal) ([]domain.RemoteHost, error) {
	items := []domain.RemoteHost{}
	for _, host := range s.hosts {
		if host.UserID == principal.UserID {
			items = append(items, host)
		}
	}
	return items, nil
}
func (s *remoteHostFakeStore) UpsertRemoteHost(ctx context.Context, principal domain.Principal, host domain.RemoteHost, limit int) error {
	listed, _ := s.ListRemoteHosts(ctx, principal)
	if _, known := s.hosts[principal.UserID+":"+host.HostID]; !known && (len(listed) >= limit || s.filledMeanwhile) {
		return postgres.ErrRemoteHostLimit
	}
	host.UserID = principal.UserID
	s.hosts[principal.UserID+":"+host.HostID] = host
	return nil
}
func (s *remoteHostFakeStore) DeleteRemoteHost(_ context.Context, principal domain.Principal, hostID string) error {
	delete(s.hosts, principal.UserID+":"+hostID)
	return nil
}
func (s *remoteHostFakeStore) FindRemoteHostRegistrations(_ context.Context, hostID string) ([]domain.RemoteHost, error) {
	items := []domain.RemoteHost{}
	for _, host := range s.hosts {
		if host.HostID == hostID {
			items = append(items, host)
		}
	}
	return items, nil
}
func (s *remoteHostFakeStore) UpdateRemoteHostAddress(_ context.Context, userID, hostID, address string) error {
	host := s.hosts[userID+":"+hostID]
	host.URL = address
	s.hosts[userID+":"+hostID] = host
	return nil
}

func hostRequest(method, hostID, body, userID string) *http.Request {
	r := httptest.NewRequest(method, "/api/cloud/v1/me/hosts/"+hostID, strings.NewReader(body))
	ctx := chi.NewRouteContext()
	ctx.URLParams.Add("hostId", hostID)
	return r.WithContext(context.WithValue(context.WithValue(r.Context(), chi.RouteCtxKey, ctx), principalKey, domain.Principal{UserID: userID}))
}

func TestAccountRemoteHostsAreOwnerScopedAndEncrypted(t *testing.T) {
	key := make([]byte, 32)
	cipher, err := secrets.New(key)
	if err != nil {
		t.Fatal(err)
	}
	store := &remoteHostFakeStore{hosts: map[string]domain.RemoteHost{}}
	s := New(Options{Store: store, SecretCipher: cipher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	hostID := "h_00000000-0000-0000-0000-000000000001"
	token := strings.Repeat("a", 64)
	owner := "00000000-0000-0000-0000-000000000002"
	other := "00000000-0000-0000-0000-000000000003"
	w := httptest.NewRecorder()
	s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"VM","url":"https://vm.example.com:443","token":"`+token+`"}`, owner))
	if w.Code != http.StatusNoContent {
		t.Fatalf("put: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(string(store.hosts[owner+":"+hostID].EncryptedToken), token) {
		t.Fatal("token stored in plaintext")
	}

	w = httptest.NewRecorder()
	s.listRemoteHosts(w, hostRequest(http.MethodGet, "", "", owner))
	if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), token) || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("owner list: %d %s", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	s.listRemoteHosts(w, hostRequest(http.MethodGet, "", "", other))
	if w.Code != http.StatusOK || strings.Contains(w.Body.String(), token) {
		t.Fatalf("other user saw host: %s", w.Body.String())
	}
	w = httptest.NewRecorder()
	s.deleteRemoteHost(w, hostRequest(http.MethodDelete, hostID, "", other))
	if _, ok := store.hosts[owner+":"+hostID]; !ok {
		t.Fatal("other user deleted host")
	}
}

func TestAccountRemoteHostRejectsInvalidCredential(t *testing.T) {
	cipher, _ := secrets.New(make([]byte, 32))
	store := &remoteHostFakeStore{hosts: map[string]domain.RemoteHost{}}
	s := New(Options{Store: store, SecretCipher: cipher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	w := httptest.NewRecorder()
	s.putRemoteHost(w, hostRequest(http.MethodPut, "h_00000000-0000-0000-0000-000000000001", `{"label":"VM","url":"https://vm.example.com","token":"short"}`, "00000000-0000-0000-0000-000000000002"))
	if w.Code != http.StatusUnprocessableEntity || len(store.hosts) != 0 {
		t.Fatalf("invalid token accepted: %d", w.Code)
	}
}

func TestHostCanPublishRotatedAddressWithScopedDigest(t *testing.T) {
	cipher, _ := secrets.New(make([]byte, 32))
	store := &remoteHostFakeStore{hosts: map[string]domain.RemoteHost{}}
	s := New(Options{Store: store, SecretCipher: cipher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	hostID := "h_00000000-0000-0000-0000-000000000001"
	owner := "00000000-0000-0000-0000-000000000002"
	token := strings.Repeat("a", 64)
	w := httptest.NewRecorder()
	s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"VM","url":"https://old.example.com","token":"`+token+`"}`, owner))
	if w.Code != http.StatusNoContent {
		t.Fatalf("registration: %d", w.Code)
	}
	hash := sha256.Sum256([]byte(token))
	for _, tc := range []struct {
		bearer string
		want   int
	}{{strings.Repeat("b", 64), http.StatusUnauthorized}, {hex.EncodeToString(hash[:]), http.StatusNoContent}} {
		r := hostRequest(http.MethodPost, hostID, `{"url":"https://new.example.com:443"}`, owner)
		r.Header.Set("Authorization", "Bearer "+tc.bearer)
		w := httptest.NewRecorder()
		s.updateRemoteHostAddress(w, r)
		if w.Code != tc.want {
			t.Fatalf("publish: got %d want %d: %s", w.Code, tc.want, w.Body.String())
		}
	}
	if store.hosts[owner+":"+hostID].URL != "https://new.example.com:443" {
		t.Fatal("rotated address was not saved")
	}
}

type remoteHostTunnelFakeStore struct {
	*remoteHostFakeStore
	tunnels map[string]domain.RemoteHostTunnel
}

func (s *remoteHostTunnelFakeStore) GetRemoteHostTunnel(_ context.Context, hostID string) (domain.RemoteHostTunnel, bool, error) {
	t, ok := s.tunnels[hostID]
	return t, ok, nil
}
func (s *remoteHostTunnelFakeStore) SaveRemoteHostTunnel(_ context.Context, t domain.RemoteHostTunnel) error {
	t.UpdatedAt = time.Now()
	s.tunnels[t.HostID] = t
	return nil
}
func (s *remoteHostTunnelFakeStore) TouchRemoteHostTunnel(context.Context, string, time.Time) error {
	return nil
}
func (s *remoteHostTunnelFakeStore) DeleteRemoteHostTunnel(_ context.Context, hostID, tunnelID string) error {
	if s.tunnels[hostID].TunnelID == tunnelID {
		delete(s.tunnels, hostID)
	}
	return nil
}

// fakeTunnelAPI is the smallest Cloudflare that provisions and deletes one
// tunnel; hosttunnel's own tests cover the API in depth.
type fakeTunnelAPI struct {
	mu      sync.Mutex
	calls   int
	tunnel  string
	deleted bool
	down    bool
}

func (f *fakeTunnelAPI) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if f.down {
		w.WriteHeader(http.StatusServiceUnavailable)
		_ = json.NewEncoder(w).Encode(map[string]any{"success": false, "errors": []any{map[string]any{"code": 1000, "message": "down"}}})
		return
	}
	var result any = []any{}
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/cfd_tunnel"):
		f.tunnel = "tun-1"
		result = map[string]string{"id": f.tunnel}
	case strings.HasSuffix(r.URL.Path, "/token"):
		result = "run-token-" + f.tunnel
	case r.Method == http.MethodDelete && strings.HasSuffix(r.URL.Path, "/cfd_tunnel/"+f.tunnel):
		f.deleted = true
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}

func (f *fakeTunnelAPI) snapshot() (int, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, f.deleted
}

func newTunnelServer(t *testing.T) (*Server, *remoteHostTunnelFakeStore, *fakeTunnelAPI) {
	t.Helper()
	cipher, _ := secrets.New(make([]byte, 32))
	api := &fakeTunnelAPI{}
	srv := httptest.NewServer(api)
	t.Cleanup(srv.Close)
	store := &remoteHostTunnelFakeStore{remoteHostFakeStore: &remoteHostFakeStore{hosts: map[string]domain.RemoteHost{}}, tunnels: map[string]domain.RemoteHostTunnel{}}
	s := New(Options{Store: store, SecretCipher: cipher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		HostTunnels: &hosttunnel.Cloudflare{AccountID: "acct", ZoneID: "zone-1", Zone: "example.dev", APIToken: "t", BaseURL: srv.URL}})
	return s, store, api
}

func requestTunnel(s *Server, hostID, bearer, body string) *httptest.ResponseRecorder {
	r := hostRequest(http.MethodPost, hostID, body, "")
	r.Header.Set("Authorization", "Bearer "+bearer)
	w := httptest.NewRecorder()
	s.remoteHostTunnel(w, r)
	return w
}

func TestLinkedHostGetsOneFixedTunnel(t *testing.T) {
	s, store, api := newTunnelServer(t)
	hostID := "h_00000000-0000-0000-0000-000000000001"
	owner := "00000000-0000-0000-0000-000000000002"
	token := strings.Repeat("a", 64)
	sum := sha256.Sum256([]byte(token))
	digest := hex.EncodeToString(sum[:])
	fixed := "https://" + s.hostTunnels.Hostname(hostID) + ":443"

	// While Cloudflare cannot make the tunnel, the host is not listed: no
	// device is told a name that has no DNS record.
	api.down = true
	w := httptest.NewRecorder()
	s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+token+`"}`, owner))
	if _, listed := store.hosts[owner+":"+hostID]; w.Code != http.StatusBadGateway || listed {
		t.Fatalf("register while Cloudflare is down: %d, listed %v", w.Code, listed)
	}
	api.down = false

	// Registered before the machine asks, the tunnel and its record are made
	// first and the host is listed at its fixed address.
	w = httptest.NewRecorder()
	s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+token+`"}`, owner))
	if _, made := store.tunnels[hostID]; w.Code != http.StatusNoContent || !made || store.hosts[owner+":"+hostID].URL != fixed {
		t.Fatalf("register: %d %s url=%q, tunnel made %v", w.Code, w.Body.String(), store.hosts[owner+":"+hostID].URL, made)
	}

	if w := requestTunnel(s, hostID, strings.Repeat("b", 64), `{}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong digest: %d", w.Code)
	}
	w = requestTunnel(s, hostID, digest, `{}`)
	var got struct{ Hostname, RunToken string }
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || "https://"+got.Hostname+":443" != fixed || got.RunToken != "run-token-tun-1" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("tunnel: %d %s", w.Code, w.Body.String())
	}
	if strings.Contains(string(store.tunnels[hostID].EncryptedRunToken), got.RunToken) {
		t.Fatal("run token stored in plaintext")
	}

	// The machine's first request, later starts, and a repair right after
	// provisioning cost no Cloudflare call.
	calls, _ := api.snapshot()
	for _, body := range []string{`{}`, `{}`, `{"repair":true}`} {
		if w := requestTunnel(s, hostID, digest, body); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "run-token-tun-1") {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if after, _ := api.snapshot(); after != calls {
		t.Fatalf("stored tunnel called Cloudflare %d times", after-calls)
	}
	stale := store.tunnels[hostID]
	stale.UpdatedAt = time.Now().Add(-time.Hour)
	store.tunnels[hostID] = stale
	if w := requestTunnel(s, hostID, digest, `{"repair":true}`); w.Code != http.StatusOK {
		t.Fatalf("repair: %d", w.Code)
	}
	if after, _ := api.snapshot(); after == calls {
		t.Fatal("repair did not ask Cloudflare")
	}
}

func TestRemovingTheLastRegistrationReleasesTheTunnel(t *testing.T) {
	s, store, api := newTunnelServer(t)
	hostID := "h_00000000-0000-0000-0000-000000000001"
	owners := []string{"00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}
	token := strings.Repeat("a", 64)
	for _, owner := range owners {
		w := httptest.NewRecorder()
		s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+token+`"}`, owner))
		if w.Code != http.StatusNoContent {
			t.Fatalf("register: %d", w.Code)
		}
	}
	sum := sha256.Sum256([]byte(token))
	if w := requestTunnel(s, hostID, hex.EncodeToString(sum[:]), `{}`); w.Code != http.StatusOK {
		t.Fatalf("tunnel: %d", w.Code)
	}
	released := func() bool {
		_, kept := store.tunnels[hostID]
		_, deleted := api.snapshot()
		return deleted && !kept
	}
	for i, owner := range owners {
		w := httptest.NewRecorder()
		s.deleteRemoteHost(w, hostRequest(http.MethodDelete, hostID, "", owner))
		if w.Code != http.StatusNoContent {
			t.Fatalf("delete: %d", w.Code)
		}
		// Released before the removal answers, so a host added back next gets a new tunnel.
		if last := i == len(owners)-1; released() != last {
			t.Fatalf("after removal %d: released %v", i+1, !last)
		}
	}
}

func TestFixedAddressesNeedCloudflare(t *testing.T) {
	cipher, _ := secrets.New(make([]byte, 32))
	store := &remoteHostTunnelFakeStore{remoteHostFakeStore: &remoteHostFakeStore{hosts: map[string]domain.RemoteHost{}}, tunnels: map[string]domain.RemoteHostTunnel{}}
	s := New(Options{Store: store, SecretCipher: cipher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	hostID := "h_00000000-0000-0000-0000-000000000001"
	if w := requestTunnel(s, hostID, strings.Repeat("a", 64), `{}`); w.Code != http.StatusServiceUnavailable {
		t.Fatalf("tunnel without Cloudflare: %d", w.Code)
	}
	w := httptest.NewRecorder()
	s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+strings.Repeat("a", 64)+`"}`, "00000000-0000-0000-0000-000000000002"))
	if w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("registration without an address: %d", w.Code)
	}
}

func TestAFullAccountListsNoMoreMachines(t *testing.T) {
	s, store, api := newTunnelServer(t)
	owner := "00000000-0000-0000-0000-000000000002"
	hostID := func(i int) string { return fmt.Sprintf("h_00000000-0000-0000-0000-%012d", i) }
	register := func(id string) int {
		w := httptest.NewRecorder()
		s.putRemoteHost(w, hostRequest(http.MethodPut, id, `{"label":"Mac","token":"`+strings.Repeat("a", 64)+`"}`, owner))
		return w.Code
	}
	for i := range domain.MaxRemoteHostsPerAccount {
		store.hosts[owner+":"+hostID(i)] = domain.RemoteHost{HostID: hostID(i), UserID: owner}
	}
	calls, _ := api.snapshot()
	if code := register(hostID(domain.MaxRemoteHostsPerAccount)); code != http.StatusConflict {
		t.Fatalf("new machine on a full account: %d", code)
	}
	if after, _ := api.snapshot(); after != calls || len(store.tunnels) != 0 {
		t.Fatalf("a refused machine cost %d Cloudflare calls and %d tunnels", after-calls, len(store.tunnels))
	}
	// A machine the account lists can still register again.
	if code := register(hostID(0)); code != http.StatusNoContent {
		t.Fatalf("listed machine on a full account: %d", code)
	}

	// Filled by a concurrent registration after the check: the tunnel made
	// for the refused machine is deleted again.
	delete(store.hosts, owner+":"+hostID(1))
	store.filledMeanwhile = true
	if code := register(hostID(domain.MaxRemoteHostsPerAccount)); code != http.StatusConflict {
		t.Fatalf("machine refused after its tunnel was made: %d", code)
	}
	if _, deleted := api.snapshot(); !deleted {
		t.Fatal("refused machine's tunnel was not deleted in Cloudflare")
	}
	if _, kept := store.tunnels[hostID(domain.MaxRemoteHostsPerAccount)]; kept {
		t.Fatal("refused machine kept its tunnel row")
	}
}
