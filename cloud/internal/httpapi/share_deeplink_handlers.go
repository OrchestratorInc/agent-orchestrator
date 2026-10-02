package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// sessionShareDeepLinkTTL bounds how long a minted deep link stays redeemable.
// It is deliberately short: the link is meant to be opened right after it is
// sent, and the secret travels over chat/QR.
const sessionShareDeepLinkTTL = 10 * time.Minute

// sessionShareDeepLink is the desktop deep link for a session share. The secret
// rides in the fragment so it is never part of a URL path that proxies or
// history might log.
func sessionShareDeepLink(orgID, linkID, token string) string {
	return "ao-app://share/" + orgID + "/" + linkID + "#" + token
}

type sessionShareDeepLinkResponse struct {
	ID        string    `json:"id"`
	OrgID     string    `json:"orgId"`
	ProjectID string    `json:"projectId"`
	SessionID string    `json:"sessionId"`
	Recipient string    `json:"recipient"`
	Role      string    `json:"role"`
	ExpiresAt time.Time `json:"expiresAt"`
	DeepLink  string    `json:"deepLink"`
}

type createSessionShareDeepLinkRequest struct {
	RecipientEmail string `json:"recipientEmail"`
	// Access is "view" (read-only, the default) or "interact" (the recipient
	// can type in the terminal, message the agent, and edit files).
	Access string `json:"access,omitempty"`
}

func (s *Server) createSessionShareDeepLink(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	var request createSessionShareDeepLinkRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body is invalid.")
		return
	}
	recipient := strings.ToLower(strings.TrimSpace(request.RecipientEmail))
	if !validEmail(recipient) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "recipientEmail must be a valid email address.")
		return
	}
	access := postgres.SessionShareAccess(strings.TrimSpace(request.Access))
	if access == "" {
		access = postgres.SessionShareView
	}
	if access != postgres.SessionShareView && access != postgres.SessionShareInteract {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "access must be view or interact.")
		return
	}
	link, token, err := s.store.CreateSessionShareDeepLink(
		r.Context(), principalFrom(r), orgID, sessionID, recipient, access, sessionShareDeepLinkTTL,
	)
	if errors.Is(err, postgres.ErrInvalid) {
		writeError(w, r, http.StatusUnprocessableEntity, "validation_error", "You cannot share a session with yourself.")
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	response := sessionShareDeepLinkResponse{
		ID:        link.ID,
		OrgID:     link.OrgID,
		ProjectID: link.ProjectID,
		SessionID: link.SessionID,
		Recipient: recipient,
		Role:      link.Role,
		DeepLink:  sessionShareDeepLink(link.OrgID, link.ID, token),
	}
	if link.ExpiresAt != nil {
		response.ExpiresAt = *link.ExpiresAt
	}
	writeJSON(w, http.StatusCreated, map[string]any{"share": response})
}

type sessionShareDeepLinkRedeemRequest struct {
	OrgID  string `json:"orgId"`
	LinkID string `json:"linkId"`
	Token  string `json:"token"`
}

type sessionShareInviteResponse struct {
	LinkID       string    `json:"linkId"`
	OrgID        string    `json:"orgId"`
	ProjectID    string    `json:"projectId"`
	SessionID    string    `json:"sessionId"`
	ProjectName  string    `json:"projectName"`
	SessionName  string    `json:"sessionName"`
	InviterEmail string    `json:"inviterEmail"`
	InviterName  string    `json:"inviterName"`
	Role         string    `json:"role"`
	ExpiresAt    time.Time `json:"expiresAt"`
}

// decodeShareDeepLinkRequest applies the shared rate limit and input checks
// for preview and redeem. Malformed input is reported as the same generic 403
// as every other refusal, so the endpoint reveals nothing about which check
// failed.
func (s *Server) decodeShareDeepLinkRequest(w http.ResponseWriter, r *http.Request) (sessionShareDeepLinkRedeemRequest, bool) {
	var request sessionShareDeepLinkRedeemRequest
	if !s.shareLinkLimiter.allow("share|"+principalFrom(r).UserID, time.Now()) {
		w.Header().Set("Retry-After", "60")
		writeError(w, r, http.StatusTooManyRequests, "rate_limited", "Too many share link attempts. Try again in a minute.")
		return request, false
	}
	if err := decodeJSON(w, r, &request); err != nil ||
		requireUUID(strings.TrimSpace(request.OrgID), "orgId") != nil ||
		requireUUID(strings.TrimSpace(request.LinkID), "linkId") != nil ||
		strings.TrimSpace(request.Token) == "" {
		writeShareDenied(w, r)
		return request, false
	}
	request.OrgID = strings.TrimSpace(request.OrgID)
	request.LinkID = strings.TrimSpace(request.LinkID)
	request.Token = strings.TrimSpace(request.Token)
	return request, true
}

func writeShareDenied(w http.ResponseWriter, r *http.Request) {
	writeError(w, r, http.StatusForbidden, "share_denied", "This share link is invalid, expired, or not addressed to you.")
}

func (s *Server) previewSessionShareDeepLink(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeShareDeepLinkRequest(w, r)
	if !ok {
		return
	}
	invite, err := s.store.PreviewSessionShareDeepLink(r.Context(), principalFrom(r), request.OrgID, request.LinkID, request.Token)
	if errors.Is(err, postgres.ErrForbidden) {
		writeShareDenied(w, r)
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"invite": sessionShareInviteResponse{
		LinkID:       invite.LinkID,
		OrgID:        invite.OrgID,
		ProjectID:    invite.ProjectID,
		SessionID:    invite.SessionID,
		ProjectName:  invite.ProjectName,
		SessionName:  invite.SessionName,
		InviterEmail: invite.InviterEmail,
		InviterName:  invite.InviterName,
		Role:         invite.Role,
		ExpiresAt:    invite.ExpiresAt,
	}})
}

func (s *Server) redeemSessionShareDeepLink(w http.ResponseWriter, r *http.Request) {
	request, ok := s.decodeShareDeepLinkRequest(w, r)
	if !ok {
		return
	}
	shared, err := s.store.RedeemSessionShareDeepLink(r.Context(), principalFrom(r), request.OrgID, request.LinkID, request.Token)
	if errors.Is(err, postgres.ErrForbidden) {
		writeShareDenied(w, r)
		return
	}
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"shared": toSharedProjectResponse(shared)})
}

// leaveSharedSession removes a session someone shared with the caller from
// their own list by revoking the caller's grant. The owner's session and every
// other collaborator's access are untouched.
func (s *Server) leaveSharedSession(w http.ResponseWriter, r *http.Request) {
	grantID := chi.URLParam(r, "grantId")
	if requireUUID(grantID, "grantId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "grantId must be a UUID.")
		return
	}
	if err := s.store.LeaveSharedSession(r.Context(), principalFrom(r), grantID); err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
