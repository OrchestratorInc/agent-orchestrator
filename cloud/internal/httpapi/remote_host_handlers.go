package httpapi

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type remoteHostStore interface {
	ListRemoteHosts(context.Context, domain.Principal) ([]domain.RemoteHost, error)
	UpsertRemoteHost(context.Context, domain.Principal, domain.RemoteHost, int) error
	DeleteRemoteHost(context.Context, domain.Principal, string) error
}

type remoteHostAddressStore interface {
	FindRemoteHostRegistrations(context.Context, string) ([]domain.RemoteHost, error)
	UpdateRemoteHostAddress(context.Context, string, string, string) error
}

type remoteHostTunnelStore interface {
	GetRemoteHostTunnel(context.Context, string) (domain.RemoteHostTunnel, bool, error)
	SaveRemoteHostTunnel(context.Context, domain.RemoteHostTunnel) error
	TouchRemoteHostTunnel(context.Context, string, time.Time) error
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
	tunnels := s.tunnelStore()
	secret, err := hex.DecodeString(input.Token)
	if !validRemoteHostID(hostID) || input.Label == "" || len(input.Label) > 64 || err != nil || len(secret) != 32 ||
		(input.URL != "" && !validRemoteAddress(input.URL)) || (input.URL == "" && tunnels == nil) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "The remote host connection is invalid.")
		return
	}
	defer clear(secret)
	principal := principalFrom(r)
	// Checked before any tunnel is made, so a full account costs no Cloudflare call.
	listed, err := store.ListRemoteHosts(r.Context(), principal)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if len(listed) >= domain.MaxRemoteHostsPerAccount && !slices.ContainsFunc(listed, func(host domain.RemoteHost) bool { return host.HostID == hostID }) {
		writeRemoteHostLimit(w, r)
		return
	}
	// A machine on this AO Cloud registers before its tunnel exists, so it can
	// leave the address out. The tunnel and its DNS record are made here,
	// before any device is told the address, so no device looks the name up
	// before it exists and caches the miss. Written the way the machine's
	// address publish writes it, so that publish does not rewrite the record
	// and reconnect every device.
	if input.URL == "" {
		hostname, _, ok := s.hostTunnel(w, r, tunnels, hostID, false)
		if !ok {
			return
		}
		input.URL = "https://" + hostname + ":443"
	}
	encrypted, nonce, err := s.secretCipher.Encrypt([]byte(input.Token), "remote-host:"+principal.UserID+":"+hostID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The remote host credential could not be stored.")
		return
	}
	if err := store.UpsertRemoteHost(r.Context(), principal, domain.RemoteHost{
		HostID: hostID, Label: input.Label, URL: input.URL, EncryptedToken: encrypted, TokenNonce: nonce,
	}, domain.MaxRemoteHostsPerAccount); err != nil {
		if errors.Is(err, postgres.ErrRemoteHostLimit) {
			// A concurrent registration filled the account after the check
			// above: drop the tunnel made for this one.
			s.releaseUnregisteredTunnel(r.Context(), hostID)
			writeRemoteHostLimit(w, r)
			return
		}
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writeRemoteHostLimit(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusConflict, "remote_host_limit_reached",
		"An account can list up to "+strconv.Itoa(domain.MaxRemoteHostsPerAccount)+" machines. Remove one to add another.")
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
	s.releaseUnregisteredTunnel(r.Context(), hostID)
	w.WriteHeader(http.StatusNoContent)
}

// releaseUnregisteredTunnel deletes hostID's tunnel once no account lists the
// host. It finishes before the removal answers, so a host added back right
// after gets a new tunnel rather than the one being deleted. A failure is left
// to the daily sweep.
func (s *Server) releaseUnregisteredTunnel(ctx context.Context, hostID string) {
	registrations, ok := s.store.(remoteHostAddressStore)
	tunnels := s.tunnelStore()
	if !ok || tunnels == nil {
		return
	}
	// A client that hangs up mid-release must not leave half a tunnel.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
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
	tunnels := s.tunnelStore()
	if !ok || tunnels == nil {
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
	hostname, runToken, ok := s.hostTunnel(w, r, tunnels, hostID, input.Repair)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"hostname": hostname, "runToken": runToken})
}

// tunnelStore is the store's tunnel half, or nil when this AO Cloud hands out
// no fixed addresses.
func (s *Server) tunnelStore() remoteHostTunnelStore {
	tunnels, ok := s.store.(remoteHostTunnelStore)
	if !ok || s.hostTunnels == nil || s.secretCipher == nil {
		return nil
	}
	return tunnels
}

// hostTunnel returns hostID's tunnel, making it and its DNS record on first
// use. repair remakes a stored tunnel, at most once a minute. It writes the
// error response itself when it fails.
func (s *Server) hostTunnel(w http.ResponseWriter, r *http.Request, tunnels remoteHostTunnelStore, hostID string, repair bool) (hostname, runToken string, ok bool) {
	stored, found, err := tunnels.GetRemoteHostTunnel(r.Context(), hostID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return "", "", false
	}
	aad := "remote-host-tunnel:" + hostID
	if found && (!repair || time.Since(stored.UpdatedAt) < minTunnelRepairInterval) {
		token, err := s.secretCipher.Decrypt(stored.EncryptedRunToken, stored.RunTokenNonce, aad)
		if err == nil {
			_ = tunnels.TouchRemoteHostTunnel(r.Context(), hostID, time.Now())
			return stored.Hostname, string(token), true
		}
	}
	tunnel, err := s.hostTunnels.Ensure(r.Context(), hostID)
	if err != nil {
		s.logger.Warn("provision host tunnel", "host_id", hostID, "error", err)
		writeError(w, r, http.StatusBadGateway, "tunnel_unavailable", "The fixed address could not be prepared.")
		return "", "", false
	}
	encrypted, nonce, err := s.secretCipher.Encrypt([]byte(tunnel.RunToken), aad)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The tunnel credential could not be stored.")
		return "", "", false
	}
	if err := tunnels.SaveRemoteHostTunnel(r.Context(), domain.RemoteHostTunnel{
		HostID: hostID, TunnelID: tunnel.ID, Hostname: tunnel.Hostname, EncryptedRunToken: encrypted, RunTokenNonce: nonce,
	}); err != nil {
		s.writeStoreError(w, r, err)
		return "", "", false
	}
	return tunnel.Hostname, tunnel.RunToken, true
}
