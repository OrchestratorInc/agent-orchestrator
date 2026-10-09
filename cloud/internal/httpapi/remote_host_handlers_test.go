package httpapi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/secrets"
	"github.com/go-chi/chi/v5"
)

type remoteHostFakeStore struct {
	Store
	hosts map[string]domain.RemoteHost
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
func (s *remoteHostFakeStore) UpsertRemoteHost(_ context.Context, principal domain.Principal, host domain.RemoteHost) error {
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
