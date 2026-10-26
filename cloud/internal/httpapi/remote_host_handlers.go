package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type remoteHostStore interface {
	ListRemoteHosts(context.Context, domain.Principal) ([]domain.RemoteHost, error)
	UpsertRemoteHost(context.Context, domain.Principal, domain.RemoteHost) error
	DeleteRemoteHost(context.Context, domain.Principal, string) error
}

type remoteHostAddressStore interface {
	FindRemoteHostRegistrations(context.Context, string) ([]domain.RemoteHost, error)
	UpdateRemoteHostAddress(context.Context, string, string, string) error
}

type remoteHostTunnelStore interface {
	GetRemoteHostTunnel(context.Context, string) (domain.RemoteHostTunnel, bool, error)
	SaveRemoteHostTunnel(context.Context, domain.RemoteHostTunnel) error
	TouchRemoteHostTunnel(context.Context, string) error
	DeleteRemoteHostTunnel(context.Context, string, string) error
}

type remoteHostRequest struct {
	Label string `json:"label"`
	URL   string `json:"url"`
	Token string `json:"token"`
}

type remoteHostResponse struct {
	HostID    string    `json:"hostId"`
	Label     string    `json:"label"`
	URL       string    `json:"url"`
	Token     string    `json:"token"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func validRemoteHostID(id string) bool {
	if !strings.HasPrefix(id, "h_") {
		return false
	}
	_, err := uuid.Parse(strings.TrimPrefix(id, "h_"))
	return err == nil
}

func validRemoteAddress(raw string) bool {
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Hostname() != "" &&
		u.User == nil && u.RawQuery == "" && u.Fragment == "" && (u.Path == "" || u.Path == "/")
}

func (s *Server) listRemoteHosts(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(remoteHostStore)
	if !ok || s.secretCipher == nil {
		writeError(w, r, http.StatusServiceUnavailable, "remote_hosts_unavailable", "Remote host discovery is unavailable.")
		return
	}
	principal := principalFrom(r)
	hosts, err := store.ListRemoteHosts(r.Context(), principal)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	items := make([]remoteHostResponse, 0, len(hosts))
	for _, host := range hosts {
		secret, err := s.secretCipher.Decrypt(host.EncryptedToken, host.TokenNonce, "remote-host:"+principal.UserID+":"+host.HostID)
		if err != nil {
			s.logger.Error("decrypt remote host token", "error", err, "request_id", requestID(r))
			writeError(w, r, http.StatusInternalServerError, "internal_error", "A remote host credential could not be read.")
			return
		}
		items = append(items, remoteHostResponse{HostID: host.HostID, Label: host.Label, URL: host.URL, Token: string(secret), UpdatedAt: host.UpdatedAt})
		clear(secret)
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": items})
}

func (s *Server) putRemoteHost(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	store, ok := s.store.(remoteHostStore)
	if !ok || s.secretCipher == nil {
		writeError(w, r, http.StatusServiceUnavailable, "remote_hosts_unavailable", "Remote host discovery is unavailable.")
		return
	}
	hostID := chi.URLParam(r, "hostId")
	var input remoteHostRequest
	if err := decodeJSON(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	input.Label = strings.TrimSpace(input.Label)
	input.URL = strings.TrimSpace(input.URL)
	// A machine on this AO Cloud registers before its tunnel exists, so it can
	// leave the address out: its fixed address is known from the host ID, and
	// the machine fetches the tunnel next. Written the way the machine's
	// address publish writes it, so that publish does not rewrite the record
	// and reconnect every device.
	if input.URL == "" && s.hostTunnels != nil && validRemoteHostID(hostID) {
		input.URL = "https://" + s.hostTunnels.Hostname(hostID) + ":443"
	}
	secret, err := hex.DecodeString(input.Token)
	if !validRemoteHostID(hostID) || input.Label == "" || len(input.Label) > 64 || !validRemoteAddress(input.URL) || err != nil || len(secret) != 32 {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The remote host connection is invalid.")
		return
	}
	defer clear(secret)
	principal := principalFrom(r)
	encrypted, nonce, err := s.secretCipher.Encrypt([]byte(input.Token), "remote-host:"+principal.UserID+":"+hostID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The remote host credential could not be stored.")
		return
	}
	if err := store.UpsertRemoteHost(r.Context(), principal, domain.RemoteHost{
		HostID: hostID, Label: input.Label, URL: input.URL, EncryptedToken: encrypted, TokenNonce: nonce,
	}); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) deleteRemoteHost(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(remoteHostStore)
	if !ok {
		writeError(w, r, http.StatusServiceUnavailable, "remote_hosts_unavailable", "Remote host discovery is unavailable.")
		return
	}
	hostID := chi.URLParam(r, "hostId")
	if !validRemoteHostID(hostID) {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The host ID is invalid.")
		return
	}
	if err := store.DeleteRemoteHost(r.Context(), principalFrom(r), hostID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.releaseUnregisteredTunnel(context.WithoutCancel(r.Context()), hostID)
	w.WriteHeader(http.StatusNoContent)
}

// releaseUnregisteredTunnel deletes hostID's tunnel once no account lists the
// host, in the background so removing a host does not wait on Cloudflare. A
// failure is left to the daily sweep.
func (s *Server) releaseUnregisteredTunnel(ctx context.Context, hostID string) {
	registrations, ok := s.store.(remoteHostAddressStore)
	tunnels, hasTunnels := s.store.(remoteHostTunnelStore)
	if !ok || !hasTunnels || s.hostTunnels == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(ctx, time.Minute)
		defer cancel()
		if hosts, err := registrations.FindRemoteHostRegistrations(ctx, hostID); err != nil || len(hosts) > 0 {
			return
		}
		tunnel, found, err := tunnels.GetRemoteHostTunnel(ctx, hostID)
		if err != nil || !found {
			return
		}
		if err := s.hostTunnels.Delete(ctx, tunnel.TunnelID, tunnel.Hostname); err != nil {
			s.logger.Warn("release host tunnel", "host_id", hostID, "error", err)
			return
		}
		if err := tunnels.DeleteRemoteHostTunnel(ctx, hostID, tunnel.TunnelID); err != nil {
			s.logger.Warn("forget host tunnel", "host_id", hostID, "error", err)
		}
	}()
}

// The host publishes a rotated quick-tunnel address using the SHA-256 digest
// of its scoped token. The digest is not accepted by the host's own listener,
// so this outbound credential cannot be replayed to control the VM.
func (s *Server) updateRemoteHostAddress(w http.ResponseWriter, r *http.Request) {
	store, ok := s.store.(remoteHostAddressStore)
	if !ok || s.secretCipher == nil {
		writeError(w, r, http.StatusServiceUnavailable, "remote_hosts_unavailable", "Remote host discovery is unavailable.")
		return
	}
	hostID := chi.URLParam(r, "hostId")
	hosts, ok := s.remoteHostsForDigest(w, r, store, hostID)
	if !ok {
		return
	}
	var input struct {
		URL string `json:"url"`
	}
	if err := decodeJSON(w, r, &input); err != nil || !validRemoteAddress(input.URL) || !strings.HasPrefix(input.URL, "https://") {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The tunnel address is invalid.")
		return
	}
	for _, host := range hosts {
		if err := store.UpdateRemoteHostAddress(r.Context(), host.UserID, hostID, input.URL); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// remoteHostsForDigest returns the registrations whose host token hashes to
// the bearer digest. It writes the error response itself when there are none.
func (s *Server) remoteHostsForDigest(w http.ResponseWriter, r *http.Request, store remoteHostAddressStore, hostID string) ([]domain.RemoteHost, bool) {
	hash := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	digest, err := hex.DecodeString(hash)
	if !validRemoteHostID(hostID) || err != nil || len(digest) != sha256.Size {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Invalid host credential.")
		return nil, false
	}
	hosts, err := store.FindRemoteHostRegistrations(r.Context(), hostID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return nil, false
	}
	matched := []domain.RemoteHost{}
	for _, host := range hosts {
		token, err := s.secretCipher.Decrypt(host.EncryptedToken, host.TokenNonce, "remote-host:"+host.UserID+":"+hostID)
		if err != nil {
			continue
		}
		actual := sha256.Sum256(token)
		clear(token)
		if subtle.ConstantTimeCompare(actual[:], digest) == 1 {
			matched = append(matched, host)
		}
	}
	if len(matched) == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Invalid host credential.")
		return nil, false
	}
	return matched, true
}

// minTunnelRepairInterval spaces the Cloudflare calls one host can cause by
// asking for a repair.
const minTunnelRepairInterval = time.Minute

// remoteHostTunnel gives a linked machine the run token of its own tunnel, so
// its address stays fixed across restarts. The machine proves the host with
// the same digest it publishes addresses with. The tunnel is made on the
// first call and kept; repair asks Cloudflare to make it exist again, for a
// machine whose address stopped being served.
func (s *Server) remoteHostTunnel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	registrations, ok := s.store.(remoteHostAddressStore)
	tunnels, hasTunnels := s.store.(remoteHostTunnelStore)
	if !ok || !hasTunnels || s.secretCipher == nil || s.hostTunnels == nil {
		writeError(w, r, http.StatusServiceUnavailable, "fixed_addresses_unavailable", "Fixed addresses are unavailable.")
		return
	}
	hostID := chi.URLParam(r, "hostId")
	if _, ok := s.remoteHostsForDigest(w, r, registrations, hostID); !ok {
		return
	}
	var input struct {
		Repair bool `json:"repair"`
	}
	if err := decodeJSONTolerant(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	stored, found, err := tunnels.GetRemoteHostTunnel(r.Context(), hostID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	aad := "remote-host-tunnel:" + hostID
	if found && (!input.Repair || time.Since(stored.UpdatedAt) < minTunnelRepairInterval) {
		token, err := s.secretCipher.Decrypt(stored.EncryptedRunToken, stored.RunTokenNonce, aad)
		if err == nil {
			_ = tunnels.TouchRemoteHostTunnel(r.Context(), hostID)
			writeJSON(w, http.StatusOK, map[string]string{"hostname": stored.Hostname, "runToken": string(token)})
			return
		}
	}
	tunnel, err := s.hostTunnels.Ensure(r.Context(), hostID)
	if err != nil {
		s.logger.Warn("provision host tunnel", "host_id", hostID, "error", err)
		writeError(w, r, http.StatusBadGateway, "tunnel_unavailable", "The fixed address could not be prepared.")
		return
	}
	encrypted, nonce, err := s.secretCipher.Encrypt([]byte(tunnel.RunToken), aad)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The tunnel credential could not be stored.")
		return
	}
	if err := tunnels.SaveRemoteHostTunnel(r.Context(), domain.RemoteHostTunnel{
		HostID: hostID, TunnelID: tunnel.ID, Hostname: tunnel.Hostname, EncryptedRunToken: encrypted, RunTokenNonce: nonce,
	}); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hostname": tunnel.Hostname, "runToken": tunnel.RunToken})
}
