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
	"github.com/aoagents/agent-orchestrator/cloud/internal/hosttunnel"
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
	RemoteHostListed(ctx context.Context, userID, hostID string) (bool, error)
	UpdateRemoteHostAddress(context.Context, string, string, string) error
}

type remoteHostTunnelStore interface {
	GetRemoteHostTunnel(ctx context.Context, userID, hostID string) (domain.RemoteHostTunnel, bool, error)
	MarkRemoteHostTunnelLinked(ctx context.Context, userID, hostID string) error
	SaveRemoteHostTunnel(ctx context.Context, tunnel domain.RemoteHostTunnel, recordCreated bool) (time.Time, error)
	TouchRemoteHostTunnel(ctx context.Context, tunnelID string, seenAt time.Time) error
	ReleaseRemoteHostTunnel(ctx context.Context, tunnelID string) error
	DeleteRemoteHostTunnel(ctx context.Context, tunnelID string) error
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
	// Clients check both before a machine moves its account token here: a
	// listing refused after that leaves the machine in no account.
	limit, fixed := 0, s.tunnelStore() != nil
	if fixed {
		limit = domain.MaxRemoteHostsPerAccount
	}
	writeJSON(w, http.StatusOK, map[string]any{"hosts": items, "limit": limit, "fixedAddresses": fixed})
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
	// The cap guards the zone's records and the account's tunnels, so an AO
	// Cloud without fixed addresses has none. Checked before any tunnel is
	// made, so a full account costs no Cloudflare call.
	limit := 0
	if tunnels != nil {
		limit = domain.MaxRemoteHostsPerAccount
		listed, err := store.ListRemoteHosts(r.Context(), principal)
		if err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		if len(listed) >= limit && !slices.ContainsFunc(listed, func(host domain.RemoteHost) bool { return host.HostID == hostID }) {
			writeRemoteHostLimit(w, r)
			return
		}
	}
	// A machine on this AO Cloud registers before its tunnel exists, so it can
	// leave the address out. Any account gets a tunnel for a host ID it names,
	// as anyone gets a quick tunnel: its name is a hash in AO's tunnel zone,
	// and the machine cap and the write limit bound how many one account holds. The tunnel and its DNS record are made here,
	// before any device is told the address, so no device looks the name up
	// before it exists and caches the miss. Written the way the machine's
	// address publish writes it, so that publish does not rewrite the record
	// and reconnect every device.
	if tunnels != nil {
		defer s.hostTunnels.Lock(principal.UserID, hostID)()
	}
	if input.URL == "" {
		tunnel, ok := s.hostTunnel(w, r, tunnels, principal.UserID, hostID, false)
		if !ok {
			return
		}
		// A link, new or again, starts the hold on quick addresses.
		if err := tunnels.MarkRemoteHostTunnelLinked(r.Context(), principal.UserID, hostID); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
		input.URL = "https://" + tunnel.Hostname + ":443"
	}
	encrypted, nonce, err := s.secretCipher.Encrypt([]byte(input.Token), "remote-host:"+principal.UserID+":"+hostID)
	if err != nil {
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The remote host credential could not be stored.")
		return
	}
	if err := store.UpsertRemoteHost(r.Context(), principal, domain.RemoteHost{
		HostID: hostID, Label: input.Label, URL: input.URL, EncryptedToken: encrypted, TokenNonce: nonce,
	}, limit); err != nil {
		if errors.Is(err, postgres.ErrRemoteHostLimit) {
			// A concurrent registration filled the account after the check
			// above: drop the tunnel made for this one, and its address,
			// which nothing ever listed.
			s.deleteAccountTunnel(r.Context(), principal.UserID, hostID, false)
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
	principal := principalFrom(r)
	if s.tunnelStore() != nil {
		defer s.hostTunnels.Lock(principal.UserID, hostID)()
	}
	if err := store.DeleteRemoteHost(r.Context(), principal, hostID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	s.deleteAccountTunnel(r.Context(), principal.UserID, hostID, true)
	w.WriteHeader(http.StatusNoContent)
}

// deleteAccountTunnel deletes the tunnel userID's account gave hostID, once the
// account no longer lists the host. It finishes before the removal answers, so
// a host added back right after gets a new tunnel rather than the one being
// deleted. With keepAddress the DNS record stays, as a release leaves it, and
// the sweep deletes it once the host has gone unlisted for 10 minutes: a host
// added back meanwhile keeps an address no resolver saw vanish, so no phone
// caches its absence. A failure is left to the sweep. The caller holds the
// host's Lock.
func (s *Server) deleteAccountTunnel(ctx context.Context, userID, hostID string, keepAddress bool) {
	tunnels := s.tunnelStore()
	if tunnels == nil {
		return
	}
	// A client that hangs up mid-release must not leave half a tunnel.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	tunnel, found, err := tunnels.GetRemoteHostTunnel(ctx, userID, hostID)
	if err != nil || !found {
		return
	}
	if !keepAddress {
		if err := s.hostTunnels.Delete(ctx, tunnel.TunnelID, tunnel.Hostname); err != nil {
			s.logger.Warn("delete host tunnel", "host_id", hostID, "error", err)
			return
		}
		if err := tunnels.DeleteRemoteHostTunnel(ctx, tunnel.TunnelID); err != nil {
			s.logger.Warn("forget host tunnel", "host_id", hostID, "error", err)
		}
		return
	}
	if tunnel.Released {
		return
	}
	if err := s.hostTunnels.DeleteTunnel(ctx, tunnel.TunnelID); err != nil {
		s.logger.Warn("delete host tunnel", "host_id", hostID, "error", err)
		return
	}
	if err := tunnels.ReleaseRemoteHostTunnel(ctx, tunnel.TunnelID); err != nil {
		s.logger.Warn("release host tunnel", "host_id", hostID, "error", err)
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
	tunnels := s.tunnelStore()
	held := 0
	for _, host := range hosts {
		// Shortly after the account's tunnel was written, the listing stays at
		// its fixed address: a machine linked just now publishes the quick
		// address it is about to leave. Later, a quick address means the
		// machine cannot serve the fixed one, and the listing follows it.
		if tunnels != nil {
			tunnel, found, err := tunnels.GetRemoteHostTunnel(r.Context(), host.UserID, hostID)
			if err != nil {
				s.writeStoreError(w, r, err)
				return
			}
			if found && !tunnel.Released && time.Since(tunnel.UpdatedAt) < fixedListingHold && input.URL != "https://"+tunnel.Hostname+":443" {
				held++
				continue
			}
		}
		if err := store.UpdateRemoteHostAddress(r.Context(), host.UserID, hostID, input.URL); err != nil {
			s.writeStoreError(w, r, err)
			return
		}
	}
	if held == len(hosts) {
		// Nothing written: the machine sends the address again soon.
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// remoteHostsForDigest returns the registrations whose host token hashes to
// the bearer digest. It writes the error response itself when there are none.
func (s *Server) remoteHostsForDigest(w http.ResponseWriter, r *http.Request, store remoteHostAddressStore, hostID string) ([]domain.RemoteHost, bool) {
	matched, err := s.matchDigest(r.Context(), store, hostID, r.Header.Get("Authorization"))
	if err != nil {
		s.writeStoreError(w, r, err)
		return nil, false
	}
	if len(matched) == 0 {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Invalid host credential.")
		return nil, false
	}
	return matched, true
}

// matchDigest is the registrations of hostID whose host token hashes to the
// digest in authorization ("Bearer <hex>"); none for a malformed one.
func (s *Server) matchDigest(ctx context.Context, store remoteHostAddressStore, hostID, authorization string) ([]domain.RemoteHost, error) {
	digest, err := hex.DecodeString(strings.TrimPrefix(authorization, "Bearer "))
	if !validRemoteHostID(hostID) || err != nil || len(digest) != sha256.Size {
		return nil, nil
	}
	hosts, err := store.FindRemoteHostRegistrations(ctx, hostID)
	if err != nil {
		return nil, err
	}
	var matched []domain.RemoteHost
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
	return matched, nil
}

// minTunnelRepairInterval spaces the Cloudflare calls one host can cause by
// asking for a repair.
const minTunnelRepairInterval = time.Minute

// fixedListingHold is how long after its tunnel was written a listing ignores
// quick addresses: a nudged machine moves to the fixed one within seconds.
const fixedListingHold = 10 * time.Minute

// Tunnel writes per account: linking machines, a machine coming back after its
// tunnel was released, and repairs. Each costs three to five Cloudflare calls,
// and the API token's rate limit is shared by every account.
const (
	tunnelWritesPerAccount = 30
	tunnelWriteWindow      = time.Hour
)

// hostTunnelResponse is what a machine needs to serve its fixed address.
type hostTunnelResponse struct {
	Hostname string `json:"hostname"`
	RunToken string `json:"runToken"`
	// Since is when the address's DNS record was made. The machine holds a
	// newer address back from devices until resolvers can have it.
	Since time.Time `json:"since"`
	// Repaired is set when a repair request remade the tunnel. A repair asked
	// too soon after the last write is answered from the stored tunnel.
	Repaired bool `json:"repaired,omitempty"`
	tunnelID string
}

// remoteHostTunnel gives a linked machine the run token of its tunnel in the
// account that linked it, so its address stays fixed across restarts. The
// machine proves the host with the same digest it publishes addresses with.
// A device of that account could compute the digest from the host token the
// account lists. That token already controls the machine; what the run token
// adds is a connector that outlives a revoked host token, the accepted
// replica-connector risk.
// The tunnel is made on the first call and kept; repair asks Cloudflare to
// make it exist again, for a machine whose address stopped being served.
func (s *Server) remoteHostTunnel(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	registrations, ok := s.store.(remoteHostAddressStore)
	tunnels := s.tunnelStore()
	if !ok || tunnels == nil {
		writeError(w, r, http.StatusServiceUnavailable, "fixed_addresses_unavailable", "Fixed addresses are unavailable.")
		return
	}
	hostID := chi.URLParam(r, "hostId")
	hosts, ok := s.remoteHostsForDigest(w, r, registrations, hostID)
	if !ok {
		return
	}
	// Each account gets its own token from the machine, so one account holds
	// the one it presents, unless the token was shared. Then the machine stays
	// with the tunnel it has: the address publish rewrites every matching
	// listing, so picking by last write would move it back and forth.
	host := slices.MaxFunc(hosts, func(a, b domain.RemoteHost) int { return a.UpdatedAt.Compare(b.UpdatedAt) })
	for _, candidate := range hosts {
		if stored, found, err := tunnels.GetRemoteHostTunnel(r.Context(), candidate.UserID, hostID); err == nil && found && !stored.Released {
			host = candidate
			break
		}
	}
	var input struct {
		Repair bool `json:"repair"`
	}
	if err := decodeJSONTolerant(w, r, &input); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	unlock := s.hostTunnels.Lock(host.UserID, hostID)
	defer unlock()
	// Asked again under the lock: a removal that ran first must not be
	// followed by a new tunnel for a host the account no longer lists.
	listed, err := registrations.RemoteHostListed(r.Context(), host.UserID, hostID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	if !listed {
		writeError(w, r, http.StatusUnauthorized, "unauthorized", "Invalid host credential.")
		return
	}
	tunnel, ok := s.hostTunnel(w, r, tunnels, host.UserID, hostID, input.Repair)
	if !ok {
		return
	}
	// Only the machine's own fetch counts as seeing it: a desktop listing a
	// machine that never connects must not keep its record.
	_ = tunnels.TouchRemoteHostTunnel(r.Context(), tunnel.tunnelID, time.Now())
	writeJSON(w, http.StatusOK, tunnel)
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

// hostTunnel returns the tunnel userID's account gives hostID, making it and
// its DNS record on first use, and making a new tunnel under the same record
// once the sweep released the old one while the host was offline. repair
// remakes a stored tunnel, at most once a minute. It writes the error response
// itself when it fails. The caller holds the host's Lock.
func (s *Server) hostTunnel(w http.ResponseWriter, r *http.Request, tunnels remoteHostTunnelStore, userID, hostID string, repair bool) (hostTunnelResponse, bool) {
	stored, found, err := tunnels.GetRemoteHostTunnel(r.Context(), userID, hostID)
	if err != nil {
		s.writeStoreError(w, r, err)
		return hostTunnelResponse{}, false
	}
	aad := "remote-host-tunnel:" + userID + ":" + hostID
	if found && !stored.Released && (!repair || time.Since(stored.UpdatedAt) < minTunnelRepairInterval) {
		token, err := s.secretCipher.Decrypt(stored.EncryptedRunToken, stored.RunTokenNonce, aad)
		if err == nil {
			return hostTunnelResponse{Hostname: stored.Hostname, RunToken: string(token), Since: stored.CreatedAt, tunnelID: stored.TunnelID}, true
		}
	}
	// ponytail: per-replica in-memory window; move it to Postgres if AO Cloud
	// runs many replicas.
	if !s.tunnelLimiter.allow(userID, time.Now()) {
		writeError(w, r, http.StatusTooManyRequests, "tunnel_rate_limited", "This account changed fixed addresses too often. Try again later.")
		return hostTunnelResponse{}, false
	}
	// Finished even if the client hangs up, so no tunnel is left half made or
	// unrecorded.
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 60*time.Second)
	defer cancel()
	tunnel, err := s.hostTunnels.Ensure(ctx, userID, hostID)
	if err != nil {
		s.logger.Warn("provision host tunnel", "host_id", hostID, "error", err)
		writeError(w, r, http.StatusBadGateway, "tunnel_unavailable", "The fixed address could not be prepared.")
		return hostTunnelResponse{}, false
	}
	encrypted, nonce, err := s.secretCipher.Encrypt([]byte(tunnel.RunToken), aad)
	if err != nil {
		s.discardTunnel(ctx, hostID, tunnel)
		writeError(w, r, http.StatusInternalServerError, "internal_error", "The tunnel credential could not be stored.")
		return hostTunnelResponse{}, false
	}
	since, err := tunnels.SaveRemoteHostTunnel(ctx, domain.RemoteHostTunnel{
		UserID: userID, HostID: hostID, TunnelID: tunnel.ID, Hostname: tunnel.Hostname, EncryptedRunToken: encrypted, RunTokenNonce: nonce,
	}, tunnel.RecordCreated)
	if err != nil {
		s.discardTunnel(ctx, hostID, tunnel)
		s.writeStoreError(w, r, err)
		return hostTunnelResponse{}, false
	}
	return hostTunnelResponse{Hostname: tunnel.Hostname, RunToken: tunnel.RunToken, Since: since, Repaired: repair, tunnelID: tunnel.ID}, true
}

func (s *Server) discardTunnel(ctx context.Context, hostID string, tunnel hosttunnel.Tunnel) {
	if err := s.hostTunnels.Discard(ctx, tunnel); err != nil {
		s.logger.Error("unrecorded host tunnel left in Cloudflare", "host_id", hostID, "error", err)
	}
}
