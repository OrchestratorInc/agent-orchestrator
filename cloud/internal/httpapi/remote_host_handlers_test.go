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
	"slices"
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
	if _, known := s.hosts[principal.UserID+":"+host.HostID]; !known && limit > 0 && (len(listed) >= limit || s.filledMeanwhile) {
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

// remoteHostTunnelFakeStore keeps tunnels by user:host.
type remoteHostTunnelFakeStore struct {
	*remoteHostFakeStore
	tunnels map[string]domain.RemoteHostTunnel
}

func (s *remoteHostTunnelFakeStore) GetRemoteHostTunnel(_ context.Context, userID, hostID string) (domain.RemoteHostTunnel, bool, error) {
	t, ok := s.tunnels[userID+":"+hostID]
	return t, ok, nil
}
func (s *remoteHostTunnelFakeStore) SaveRemoteHostTunnel(_ context.Context, t domain.RemoteHostTunnel, recordCreated bool) (time.Time, error) {
	t.UpdatedAt = time.Now()
	t.CreatedAt = s.tunnels[t.UserID+":"+t.HostID].CreatedAt
	if recordCreated || t.CreatedAt.IsZero() {
		t.CreatedAt = t.UpdatedAt
	}
	s.tunnels[t.UserID+":"+t.HostID] = t
	return t.CreatedAt, nil
}
func (s *remoteHostTunnelFakeStore) MarkRemoteHostTunnelLinked(_ context.Context, userID, hostID string) error {
	if t, ok := s.tunnels[userID+":"+hostID]; ok {
		t.UpdatedAt = time.Now()
		s.tunnels[userID+":"+hostID] = t
	}
	return nil
}
func (s *remoteHostTunnelFakeStore) TouchRemoteHostTunnel(context.Context, string, time.Time) error {
	return nil
}
func (s *remoteHostTunnelFakeStore) DeleteRemoteHostTunnel(_ context.Context, tunnelID string) error {
	for key, t := range s.tunnels {
		if t.TunnelID == tunnelID {
			delete(s.tunnels, key)
		}
	}
	return nil
}

// fakeTunnelAPI is the smallest Cloudflare that provisions and deletes
// tunnels; hosttunnel's own tests cover the API in depth.
type fakeTunnelAPI struct {
	mu      sync.Mutex
	calls   int
	made    int
	tunnel  string
	deleted []string
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
		f.made++
		f.tunnel = fmt.Sprintf("tun-%d", f.made)
		result = map[string]string{"id": f.tunnel}
	case strings.HasSuffix(r.URL.Path, "/token"):
		result = "run-token-" + f.tunnel
	case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/cfd_tunnel/tun-") && !strings.HasSuffix(r.URL.Path, "/connections"):
		f.deleted = append(f.deleted, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "result": result})
}

// snapshot is how many calls were made, and the tunnels deleted.
func (f *fakeTunnelAPI) snapshot() (int, []string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls, slices.Clone(f.deleted)
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
	fixed := "https://" + s.hostTunnels.Hostname(owner, hostID) + ":443"
	key := owner + ":" + hostID

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
	if _, made := store.tunnels[key]; w.Code != http.StatusNoContent || !made || store.hosts[owner+":"+hostID].URL != fixed {
		t.Fatalf("register: %d %s url=%q, tunnel made %v", w.Code, w.Body.String(), store.hosts[owner+":"+hostID].URL, made)
	}

	if w := requestTunnel(s, hostID, strings.Repeat("b", 64), `{}`); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong digest: %d", w.Code)
	}
	w = requestTunnel(s, hostID, digest, `{}`)
	var got struct {
		Hostname, RunToken string
		Since              time.Time
	}
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || "https://"+got.Hostname+":443" != fixed || got.RunToken != "run-token-tun-1" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("tunnel: %d %s", w.Code, w.Body.String())
	}
	// The machine waits on a DNS record this new before handing it out.
	if !got.Since.Equal(store.tunnels[key].CreatedAt) || time.Since(got.Since) > time.Minute {
		t.Fatalf("since %v, record made %v", got.Since, store.tunnels[key].CreatedAt)
	}
	if strings.Contains(string(store.tunnels[key].EncryptedRunToken), got.RunToken) {
		t.Fatal("run token stored in plaintext")
	}

	// The machine's first request, later starts, and a repair right after
	// provisioning cost no Cloudflare call.
	calls, _ := api.snapshot()
	for _, body := range []string{`{}`, `{}`, `{"repair":true}`} {
		// A repair this soon is answered from the stored tunnel, and says so.
		if w := requestTunnel(s, hostID, digest, body); w.Code != http.StatusOK || !strings.Contains(w.Body.String(), "run-token-tun-1") ||
			strings.Contains(w.Body.String(), "repaired") {
			t.Fatalf("%s: %d %s", body, w.Code, w.Body.String())
		}
	}
	if after, _ := api.snapshot(); after != calls {
		t.Fatalf("stored tunnel called Cloudflare %d times", after-calls)
	}
	stale := store.tunnels[key]
	stale.UpdatedAt = time.Now().Add(-time.Hour)
	store.tunnels[key] = stale
	w = requestTunnel(s, hostID, digest, `{"repair":true}`)
	if w.Code != http.StatusOK {
		t.Fatalf("repair: %d", w.Code)
	}
	if after, _ := api.snapshot(); after == calls {
		t.Fatal("repair did not ask Cloudflare")
	}
	if !strings.Contains(w.Body.String(), `"repaired":true`) {
		t.Fatalf("a repair that remade the tunnel did not say so: %s", w.Body.String())
	}

	// Released while the machine was offline: its next request gets a new
	// tunnel at the same address, never the deleted tunnel's token.
	before := store.tunnels[key]
	released := before
	released.Released = true
	store.tunnels[key] = released
	w = requestTunnel(s, hostID, digest, `{}`)
	_ = json.Unmarshal(w.Body.Bytes(), &got)
	if w.Code != http.StatusOK || "https://"+got.Hostname+":443" != fixed || store.tunnels[key].TunnelID == before.TunnelID ||
		got.RunToken != "run-token-"+store.tunnels[key].TunnelID || store.tunnels[key].Released {
		t.Fatalf("after release: %d %s, row %+v", w.Code, w.Body.String(), store.tunnels[key])
	}
}

// Anyone can list any host ID in their own account with a token they chose.
// That registration must never reach the tunnel the host's own account uses:
// a connector run with its token would take a share of the host's traffic,
// connection password included.
func TestAnotherAccountListingTheHostGetsItsOwnTunnel(t *testing.T) {
	s, store, _ := newTunnelServer(t)
	hostID := "h_00000000-0000-0000-0000-000000000001"
	victim, attacker := "00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"
	tokens := map[string]string{victim: strings.Repeat("a", 64), attacker: strings.Repeat("c", 64)}
	got := map[string]struct{ Hostname, RunToken string }{}
	for _, owner := range []string{victim, attacker} {
		w := httptest.NewRecorder()
		s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+tokens[owner]+`"}`, owner))
		if w.Code != http.StatusNoContent {
			t.Fatalf("register: %d", w.Code)
		}
		sum := sha256.Sum256([]byte(tokens[owner]))
		w = requestTunnel(s, hostID, hex.EncodeToString(sum[:]), `{}`)
		var tunnel struct{ Hostname, RunToken string }
		if err := json.Unmarshal(w.Body.Bytes(), &tunnel); err != nil || w.Code != http.StatusOK {
			t.Fatalf("tunnel: %d %s", w.Code, w.Body.String())
		}
		got[owner] = tunnel
	}
	if got[attacker].Hostname == got[victim].Hostname || got[attacker].RunToken == got[victim].RunToken {
		t.Fatalf("the attacker's registration reached the victim's tunnel: %+v", got)
	}
	if store.hosts[victim+":"+hostID].URL != "https://"+got[victim].Hostname+":443" {
		t.Fatalf("victim lists %q", store.hosts[victim+":"+hostID].URL)
	}
}

// A machine just linked can publish its quick address before it moves to the
// fixed one its account lists it at; that must not overwrite the listing.
func TestAStandingTunnelKeepsTheListingAtItsAddress(t *testing.T) {
	s, store, _ := newTunnelServer(t)
	hostID := "h_00000000-0000-0000-0000-000000000001"
	owner := "00000000-0000-0000-0000-000000000002"
	token := strings.Repeat("a", 64)
	w := httptest.NewRecorder()
	s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+token+`"}`, owner))
	fixed := store.hosts[owner+":"+hostID].URL
	sum := sha256.Sum256([]byte(token))
	publish := func(url string) {
		t.Helper()
		r := hostRequest(http.MethodPost, hostID, `{"url":"`+url+`"}`, "")
		r.Header.Set("Authorization", "Bearer "+hex.EncodeToString(sum[:]))
		w := httptest.NewRecorder()
		s.updateRemoteHostAddress(w, r)
		if w.Code != http.StatusNoContent {
			t.Fatalf("publish %s: %d", url, w.Code)
		}
	}
	publish("https://quiet-river.trycloudflare.com:443")
	if got := store.hosts[owner+":"+hostID].URL; got != fixed {
		t.Fatalf("a quick address replaced the fixed listing: %q", got)
	}
	// Long after, a quick address means the machine cannot serve the fixed
	// one, and the listing follows it.
	old := store.tunnels[owner+":"+hostID]
	old.UpdatedAt = time.Now().Add(-time.Hour)
	store.tunnels[owner+":"+hostID] = old
	publish("https://quiet-river.trycloudflare.com:443")
	if got := store.hosts[owner+":"+hostID].URL; got != "https://quiet-river.trycloudflare.com:443" {
		t.Fatalf("listing = %q", got)
	}
}

// Clients check the cap and fixed addresses before a machine moves its
// account token to the account.
func TestTheHostListSaysWhatTheAccountCanList(t *testing.T) {
	read := func(s *Server) (limit int, fixed bool) {
		t.Helper()
		w := httptest.NewRecorder()
		s.listRemoteHosts(w, hostRequest(http.MethodGet, "", "", "00000000-0000-0000-0000-000000000002"))
		var body struct {
			Limit          int  `json:"limit"`
			FixedAddresses bool `json:"fixedAddresses"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || w.Code != http.StatusOK {
			t.Fatalf("list: %d %s", w.Code, w.Body.String())
		}
		return body.Limit, body.FixedAddresses
	}
	s, _, _ := newTunnelServer(t)
	if limit, fixed := read(s); limit != domain.MaxRemoteHostsPerAccount || !fixed {
		t.Fatalf("with fixed addresses: limit %d, fixed %v", limit, fixed)
	}
	cipher, _ := secrets.New(make([]byte, 32))
	plain := New(Options{Store: &remoteHostFakeStore{hosts: map[string]domain.RemoteHost{}}, SecretCipher: cipher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	if limit, fixed := read(plain); limit != 0 || fixed {
		t.Fatalf("without: limit %d, fixed %v", limit, fixed)
	}
}

func TestRemovingAHostDeletesOnlyThatAccountsTunnel(t *testing.T) {
	s, store, api := newTunnelServer(t)
	hostID := "h_00000000-0000-0000-0000-000000000001"
	owners := []string{"00000000-0000-0000-0000-000000000002", "00000000-0000-0000-0000-000000000003"}
	for _, owner := range owners {
		w := httptest.NewRecorder()
		s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+strings.Repeat("a", 64)+`"}`, owner))
		if w.Code != http.StatusNoContent {
			t.Fatalf("register: %d", w.Code)
		}
	}
	first, second := store.tunnels[owners[0]+":"+hostID], store.tunnels[owners[1]+":"+hostID]
	w := httptest.NewRecorder()
	s.deleteRemoteHost(w, hostRequest(http.MethodDelete, hostID, "", owners[0]))
	if w.Code != http.StatusNoContent {
		t.Fatalf("delete: %d", w.Code)
	}
	// Deleted before the removal answers, so a host added back next gets a new tunnel.
	_, deleted := api.snapshot()
	if _, kept := store.tunnels[owners[0]+":"+hostID]; kept || !slices.Equal(deleted, []string{first.TunnelID}) {
		t.Fatalf("after removal: kept %v, deleted %v", kept, deleted)
	}
	if store.tunnels[owners[1]+":"+hostID].TunnelID != second.TunnelID {
		t.Fatal("removing the host from one account touched another account's tunnel")
	}
}

// Each tunnel write costs several Cloudflare calls on a rate limit every
// account shares, so one account cannot make them without end.
func TestTunnelWritesAreLimitedPerAccount(t *testing.T) {
	s, _, api := newTunnelServer(t)
	s.tunnelLimiter = newFixedWindowLimiter(1, time.Hour, 16)
	register := func(owner, hostID string) int {
		w := httptest.NewRecorder()
		s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"Mac","token":"`+strings.Repeat("a", 64)+`"}`, owner))
		return w.Code
	}
	owner := "00000000-0000-0000-0000-000000000002"
	if code := register(owner, "h_00000000-0000-0000-0000-000000000001"); code != http.StatusNoContent {
		t.Fatalf("first: %d", code)
	}
	calls, _ := api.snapshot()
	if code := register(owner, "h_00000000-0000-0000-0000-000000000002"); code != http.StatusTooManyRequests {
		t.Fatalf("over the limit: %d", code)
	}
	if after, _ := api.snapshot(); after != calls {
		t.Fatalf("a refused write cost %d Cloudflare calls", after-calls)
	}
	if code := register("00000000-0000-0000-0000-000000000003", "h_00000000-0000-0000-0000-000000000002"); code != http.StatusNoContent {
		t.Fatalf("another account: %d", code)
	}
}

// The cap guards the zone and the tunnel account, so an AO Cloud without fixed
// addresses lists any number of machines.
func TestTheMachineCapNeedsFixedAddresses(t *testing.T) {
	cipher, _ := secrets.New(make([]byte, 32))
	store := &remoteHostFakeStore{hosts: map[string]domain.RemoteHost{}}
	s := New(Options{Store: store, SecretCipher: cipher, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	owner := "00000000-0000-0000-0000-000000000002"
	for i := range domain.MaxRemoteHostsPerAccount + 1 {
		hostID := fmt.Sprintf("h_00000000-0000-0000-0000-%012d", i)
		w := httptest.NewRecorder()
		s.putRemoteHost(w, hostRequest(http.MethodPut, hostID, `{"label":"VM","url":"https://vm.example.com","token":"`+strings.Repeat("a", 64)+`"}`, owner))
		if w.Code != http.StatusNoContent {
			t.Fatalf("machine %d: %d", i+1, w.Code)
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
	if _, deleted := api.snapshot(); len(deleted) != 1 {
		t.Fatalf("refused machine's tunnel was not deleted in Cloudflare: %v", deleted)
	}
	if _, kept := store.tunnels[owner+":"+hostID(domain.MaxRemoteHostsPerAccount)]; kept {
		t.Fatal("refused machine kept its tunnel row")
	}
}
