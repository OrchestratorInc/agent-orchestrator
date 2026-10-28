package httpapi

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// sessionParticipantsStore is the store surface the board reads shared
// sessions' people from.
type sessionParticipantsStore interface {
	SessionParticipants(ctx context.Context, principal domain.Principal, orgID string, sessionIDs []string) (map[string][]domain.SessionParticipant, error)
}

type sessionParticipantResponse struct {
	UserID    string `json:"userId"`
	Name      string `json:"name"`
	AvatarURL string `json:"avatarUrl,omitempty"`
	Role      string `json:"role"`
	Present   bool   `json:"present"`
	State     string `json:"state"`
}

type sharedSessionParticipantsResponse struct {
	SessionID    string                       `json:"sessionId"`
	Participants []sessionParticipantResponse `json:"participants"`
}

// listSessionParticipants answers, for the sessions a board shows, who is in
// each one that has been shared and what they are doing. Reading it does not
// make the reader present in any session, unlike a live stream.
func (s *Server) listSessionParticipants(w http.ResponseWriter, r *http.Request) {
	orgID := chi.URLParam(r, "orgId")
	if requireUUID(orgID, "orgId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId must be a UUID.")
		return
	}
	sessionIDs := r.URL.Query()["sessionId"]
	if len(sessionIDs) > postgres.MaxParticipantSessions {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "Ask for at most 100 sessions at a time.")
		return
	}
	for _, sessionID := range sessionIDs {
		if requireUUID(sessionID, "sessionId") != nil {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "Every sessionId must be a UUID.")
			return
		}
	}
	store, ok := s.store.(sessionParticipantsStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Session participants are not available.")
		return
	}
	participants, err := store.SessionParticipants(r.Context(), principalFrom(r), orgID, sessionIDs)
	if err != nil {
		s.writeStoreError(w, r, err)
		return
	}
	sessions := []sharedSessionParticipantsResponse{}
	for _, sessionID := range sessionIDs {
		people, shared := participants[sessionID]
		if !shared {
			continue
		}
		entry := sharedSessionParticipantsResponse{SessionID: sessionID, Participants: make([]sessionParticipantResponse, 0, len(people))}
		for _, person := range people {
			entry.Participants = append(entry.Participants, sessionParticipantResponse{
				UserID: person.UserID, Name: person.Name, AvatarURL: person.AvatarURL, Role: person.Role, Present: person.Present, State: person.State,
			})
		}
		sessions = append(sessions, entry)
	}
	writeJSON(w, http.StatusOK, map[string]any{"sessions": sessions})
}
