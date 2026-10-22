package httpapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

// composerStore is the composer lease surface of the store. A store without
// it serves the composer routes as not implemented.
type composerStore interface {
	AcquireComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error)
	RenewComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string, epoch int64, typing bool) (domain.ComposerLease, error)
	ReleaseComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string, epoch int64) (domain.ComposerLease, error)
	RequestComposerTurn(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error)
	LeaveComposerQueue(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error)
	TakeOverComposerLease(ctx context.Context, principal domain.Principal, orgID, sessionID string) (domain.ComposerLease, error)
}

type composerParticipantResponse struct {
	UserID string `json:"userId"`
	Name   string `json:"name"`
}

// composerLiveResponse is the lease as every participant sees it in a
// session.live frame. It leaves out the grant's remaining time, which changes
// on every renewal without anything visible changing.
type composerLiveResponse struct {
	Holder   *composerParticipantResponse  `json:"holder"`
	Epoch    int64                         `json:"epoch"`
	Waitlist []composerParticipantResponse `json:"waitlist"`
}

// composerLeaseResponse answers a composer request: the lease, whether the
// caller holds it, and how long their grant has left.
type composerLeaseResponse struct {
	composerLiveResponse
	ExpiresInMs int64 `json:"expiresInMs"`
}

func toComposerLiveResponse(lease domain.ComposerLease) composerLiveResponse {
	response := composerLiveResponse{Epoch: lease.Epoch, Waitlist: make([]composerParticipantResponse, 0, len(lease.Waitlist))}
	if lease.Holder != nil {
		response.Holder = &composerParticipantResponse{UserID: lease.Holder.UserID, Name: lease.Holder.Name}
	}
	for _, waiter := range lease.Waitlist {
		response.Waitlist = append(response.Waitlist, composerParticipantResponse{UserID: waiter.UserID, Name: waiter.Name})
	}
	return response
}

type composerGrantRequest struct {
	Epoch  int64 `json:"epoch"`
	Typing bool  `json:"typing,omitempty"`
}

// composerAction runs one composer operation and answers with the resulting
// lease and whether the caller holds it. A refusal (someone else holds it, or
// the caller's grant ended) is an ordinary answer with granted false, so the
// client gets the state it needs to show who has the composer.
func (s *Server) composerAction(
	w http.ResponseWriter,
	r *http.Request,
	needsGrant bool,
	run func(store composerStore, principal domain.Principal, orgID, sessionID string, request composerGrantRequest) (domain.ComposerLease, error),
) {
	orgID := chi.URLParam(r, "orgId")
	sessionID := chi.URLParam(r, "sessionId")
	if requireUUID(orgID, "orgId") != nil || requireUUID(sessionID, "sessionId") != nil {
		writeError(w, r, http.StatusBadRequest, "invalid_request", "orgId and sessionId must be UUIDs.")
		return
	}
	var request composerGrantRequest
	if needsGrant {
		if err := decodeJSON(w, r, &request); err != nil || request.Epoch <= 0 {
			writeError(w, r, http.StatusBadRequest, "invalid_request", "The request body must name the epoch of the grant.")
			return
		}
	}
	store, ok := s.store.(composerStore)
	if !ok {
		writeError(w, r, http.StatusNotImplemented, "not_implemented", "Shared session composer is not available.")
		return
	}
	principal := principalFrom(r)
	lease, err := run(store, principal, orgID, sessionID, request)
	if err != nil && !errors.Is(err, postgres.ErrComposerLocked) && !errors.Is(err, postgres.ErrComposerLeaseLost) {
		s.writeStoreError(w, r, err)
		return
	}
	granted := err == nil && lease.Holder != nil && lease.Holder.UserID == principal.UserID
	response := composerLeaseResponse{composerLiveResponse: toComposerLiveResponse(lease)}
	if granted {
		response.ExpiresInMs = lease.ExpiresIn.Milliseconds()
	}
	writeJSON(w, http.StatusOK, map[string]any{"composer": response, "granted": granted})
}

func (s *Server) acquireComposerLease(w http.ResponseWriter, r *http.Request) {
	s.composerAction(w, r, false, func(store composerStore, principal domain.Principal, orgID, sessionID string, _ composerGrantRequest) (domain.ComposerLease, error) {
		return store.AcquireComposerLease(r.Context(), principal, orgID, sessionID)
	})
}

func (s *Server) renewComposerLease(w http.ResponseWriter, r *http.Request) {
	s.composerAction(w, r, true, func(store composerStore, principal domain.Principal, orgID, sessionID string, request composerGrantRequest) (domain.ComposerLease, error) {
		return store.RenewComposerLease(r.Context(), principal, orgID, sessionID, request.Epoch, request.Typing)
	})
}

func (s *Server) releaseComposerLease(w http.ResponseWriter, r *http.Request) {
	s.composerAction(w, r, true, func(store composerStore, principal domain.Principal, orgID, sessionID string, request composerGrantRequest) (domain.ComposerLease, error) {
		return store.ReleaseComposerLease(r.Context(), principal, orgID, sessionID, request.Epoch)
	})
}

func (s *Server) requestComposerTurn(w http.ResponseWriter, r *http.Request) {
	s.composerAction(w, r, false, func(store composerStore, principal domain.Principal, orgID, sessionID string, _ composerGrantRequest) (domain.ComposerLease, error) {
		return store.RequestComposerTurn(r.Context(), principal, orgID, sessionID)
	})
}

func (s *Server) leaveComposerQueue(w http.ResponseWriter, r *http.Request) {
	s.composerAction(w, r, false, func(store composerStore, principal domain.Principal, orgID, sessionID string, _ composerGrantRequest) (domain.ComposerLease, error) {
		return store.LeaveComposerQueue(r.Context(), principal, orgID, sessionID)
	})
}

func (s *Server) takeOverComposerLease(w http.ResponseWriter, r *http.Request) {
	s.composerAction(w, r, false, func(store composerStore, principal domain.Principal, orgID, sessionID string, _ composerGrantRequest) (domain.ComposerLease, error) {
		return store.TakeOverComposerLease(r.Context(), principal, orgID, sessionID)
	})
}
