package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

const (
	shareTestOrgID     = "00000000-0000-0000-0000-0000000000a1"
	shareTestSessionID = "00000000-0000-0000-0000-0000000000b2"
	shareTestLinkID    = "00000000-0000-0000-0000-0000000000c3"
	shareTestUserID    = "00000000-0000-0000-0000-0000000000d4"
)

type shareDeepLinkFakeStore struct {
	Store
	createRecipient string
	createAccess    postgres.SessionShareAccess
	createTTL       time.Duration
	leaveGrantID    string
	leaveErr        error
	createErr       error
	previewErr      error
	redeemErr       error
	redeemCalls     int
}

func (s *shareDeepLinkFakeStore) CreateSessionShareDeepLink(
	_ context.Context, _ domain.Principal, orgID, sessionID, recipient string,
	access postgres.SessionShareAccess, ttl time.Duration,
) (domain.ShareLink, string, error) {
	s.createRecipient, s.createAccess, s.createTTL = recipient, access, ttl
	if s.createErr != nil {
		return domain.ShareLink{}, "", s.createErr
	}
	expires := time.Now().Add(ttl)
	return domain.ShareLink{ID: shareTestLinkID, OrgID: orgID, SessionID: sessionID, Role: "viewer", ExpiresAt: &expires}, "s3cret", nil
}

func (s *shareDeepLinkFakeStore) PreviewSessionShareDeepLink(
	context.Context, domain.Principal, string, string, string,
) (domain.SessionShareInvite, error) {
	return domain.SessionShareInvite{LinkID: shareTestLinkID, Role: "viewer", InviterName: "Dev"}, s.previewErr
}

func (s *shareDeepLinkFakeStore) RedeemSessionShareDeepLink(
	context.Context, domain.Principal, string, string, string,
) (domain.SharedProject, error) {
	s.redeemCalls++
	return domain.SharedProject{}, s.redeemErr
}

func (s *shareDeepLinkFakeStore) LeaveSharedSession(_ context.Context, _ domain.Principal, grantID string) error {
	s.leaveGrantID = grantID
	return s.leaveErr
}

func newShareDeepLinkServer(store *shareDeepLinkFakeStore) *Server {
	return New(Options{Store: store, Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
}

func shareRequest(t *testing.T, method, path string, body any, params map[string]string) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	req := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	rctx := chi.NewRouteContext()
	for key, value := range params {
		rctx.URLParams.Add(key, value)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	ctx = context.WithValue(ctx, principalKey, domain.Principal{UserID: shareTestUserID, Email: "dev@local.test"})
	return req.WithContext(ctx)
}

func TestCreateSessionShareDeepLinkReturnsReadOnlyDeepLink(t *testing.T) {
	store := &shareDeepLinkFakeStore{}
	server := newShareDeepLinkServer(store)
	recorder := httptest.NewRecorder()
	server.createSessionShareDeepLink(recorder, shareRequest(t, http.MethodPost, "/", map[string]string{
		"recipientEmail": "  Viewer@Local.Test ",
	}, map[string]string{"orgId": shareTestOrgID, "sessionId": shareTestSessionID}))

	if recorder.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", recorder.Code, recorder.Body)
	}
	if store.createRecipient != "viewer@local.test" {
		t.Errorf("recipient = %q, want normalized viewer@local.test", store.createRecipient)
	}
	if store.createAccess != postgres.SessionShareView {
		t.Errorf("access = %q, want view when omitted", store.createAccess)
	}
	if store.createTTL != sessionShareDeepLinkTTL {
		t.Errorf("ttl = %v, want %v", store.createTTL, sessionShareDeepLinkTTL)
	}
	var response struct {
		Share sessionShareDeepLinkResponse `json:"share"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode: %v", err)
	}
	want := "ao-app://share/" + shareTestOrgID + "/" + shareTestLinkID + "#s3cret"
	if response.Share.DeepLink != want || response.Share.Role != "viewer" {
		t.Errorf("share = %+v, want deepLink %q role viewer", response.Share, want)
	}
}

func TestCreateSessionShareDeepLinkValidation(t *testing.T) {
	for _, test := range []struct {
		name   string
		body   map[string]string
		params map[string]string
		err    error
		want   int
	}{
		{"bad email", map[string]string{"recipientEmail": "nope"}, map[string]string{"orgId": shareTestOrgID, "sessionId": shareTestSessionID}, nil, http.StatusUnprocessableEntity},
		{"bad session id", map[string]string{"recipientEmail": "a@b.test"}, map[string]string{"orgId": shareTestOrgID, "sessionId": "x"}, nil, http.StatusBadRequest},
		{"self share", map[string]string{"recipientEmail": "a@b.test"}, map[string]string{"orgId": shareTestOrgID, "sessionId": shareTestSessionID}, postgres.ErrInvalid, http.StatusUnprocessableEntity},
		{"not owner", map[string]string{"recipientEmail": "a@b.test"}, map[string]string{"orgId": shareTestOrgID, "sessionId": shareTestSessionID}, postgres.ErrForbidden, http.StatusForbidden},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newShareDeepLinkServer(&shareDeepLinkFakeStore{createErr: test.err})
			recorder := httptest.NewRecorder()
			server.createSessionShareDeepLink(recorder, shareRequest(t, http.MethodPost, "/", test.body, test.params))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body)
			}
		})
	}
}

// Every refusal — malformed input or any failed store check — must be the
// same generic 403 so the endpoint does not reveal which check failed.
func TestRedeemSessionShareDeepLinkDeniesGenerically(t *testing.T) {
	valid := map[string]string{"orgId": shareTestOrgID, "linkId": shareTestLinkID, "token": "s3cret"}
	for _, test := range []struct {
		name string
		body map[string]string
		err  error
	}{
		{"store refusal", valid, postgres.ErrForbidden},
		{"missing token", map[string]string{"orgId": shareTestOrgID, "linkId": shareTestLinkID}, nil},
		{"bad link id", map[string]string{"orgId": shareTestOrgID, "linkId": "x", "token": "s"}, nil},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := newShareDeepLinkServer(&shareDeepLinkFakeStore{redeemErr: test.err})
			recorder := httptest.NewRecorder()
			server.redeemSessionShareDeepLink(recorder, shareRequest(t, http.MethodPost, "/", test.body, nil))
			if recorder.Code != http.StatusForbidden || !bytes.Contains(recorder.Body.Bytes(), []byte(`"share_denied"`)) {
				t.Fatalf("status = %d body = %s, want generic 403 share_denied", recorder.Code, recorder.Body)
			}
		})
	}
}

func TestPreviewSessionShareDeepLink(t *testing.T) {
	server := newShareDeepLinkServer(&shareDeepLinkFakeStore{})
	recorder := httptest.NewRecorder()
	server.previewSessionShareDeepLink(recorder, shareRequest(t, http.MethodPost, "/", map[string]string{
		"orgId": shareTestOrgID, "linkId": shareTestLinkID, "token": "s3cret",
	}, nil))
	if recorder.Code != http.StatusOK || !bytes.Contains(recorder.Body.Bytes(), []byte(`"role":"viewer"`)) {
		t.Fatalf("status = %d body = %s", recorder.Code, recorder.Body)
	}
}

func TestShareDeepLinkAttemptsAreRateLimited(t *testing.T) {
	store := &shareDeepLinkFakeStore{redeemErr: postgres.ErrForbidden}
	server := newShareDeepLinkServer(store)
	body := map[string]string{"orgId": shareTestOrgID, "linkId": shareTestLinkID, "token": "guess"}
	var last int
	for range 11 {
		recorder := httptest.NewRecorder()
		server.redeemSessionShareDeepLink(recorder, shareRequest(t, http.MethodPost, "/", body, nil))
		last = recorder.Code
	}
	if last != http.StatusTooManyRequests {
		t.Fatalf("11th attempt status = %d, want 429", last)
	}
	if store.redeemCalls != 10 {
		t.Fatalf("store reached %d times, want 10", store.redeemCalls)
	}
}

func TestCreateSessionShareDeepLinkAccessLevels(t *testing.T) {
	params := map[string]string{"orgId": shareTestOrgID, "sessionId": shareTestSessionID}
	for _, test := range []struct {
		access string
		want   int
		stored postgres.SessionShareAccess
	}{
		{"view", http.StatusCreated, postgres.SessionShareView},
		{"interact", http.StatusCreated, postgres.SessionShareInteract},
		{"admin", http.StatusUnprocessableEntity, ""},
	} {
		t.Run(test.access, func(t *testing.T) {
			store := &shareDeepLinkFakeStore{}
			recorder := httptest.NewRecorder()
			newShareDeepLinkServer(store).createSessionShareDeepLink(recorder, shareRequest(t, http.MethodPost, "/", map[string]string{
				"recipientEmail": "viewer@local.test", "access": test.access,
			}, params))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body)
			}
			if store.createAccess != test.stored {
				t.Fatalf("store access = %q, want %q", store.createAccess, test.stored)
			}
		})
	}
}

func TestLeaveSharedSession(t *testing.T) {
	for _, test := range []struct {
		name    string
		grantID string
		err     error
		want    int
	}{
		{"own grant", shareTestLinkID, nil, http.StatusNoContent},
		{"not mine or unknown", shareTestLinkID, postgres.ErrNotFound, http.StatusNotFound},
		{"bad id", "x", nil, http.StatusBadRequest},
	} {
		t.Run(test.name, func(t *testing.T) {
			store := &shareDeepLinkFakeStore{leaveErr: test.err}
			recorder := httptest.NewRecorder()
			newShareDeepLinkServer(store).leaveSharedSession(recorder, shareRequest(t, http.MethodDelete, "/", nil, map[string]string{"grantId": test.grantID}))
			if recorder.Code != test.want {
				t.Fatalf("status = %d, want %d, body = %s", recorder.Code, test.want, recorder.Body)
			}
			if test.want == http.StatusBadRequest && store.leaveGrantID != "" {
				t.Fatal("store reached with an invalid grant id")
			}
		})
	}
}
