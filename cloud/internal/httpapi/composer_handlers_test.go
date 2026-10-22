package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
	"github.com/aoagents/agent-orchestrator/cloud/internal/postgres"
)

const (
	composerTestOrg     = "00000000-0000-4000-8000-0000000000c1"
	composerTestSession = "00000000-0000-4000-8000-0000000000c2"
	composerTestUser    = "00000000-0000-4000-8000-0000000000c3"
)

// composerTestStore answers composer operations with a fixed lease and error,
// recording what it was asked.
type composerTestStore struct {
	Store
	lease      domain.ComposerLease
	err        error
	calls      []string
	epoch      int64
	typing     bool
	sendEpoch  int64
	steerEpoch int64
	sendErr    error
}

func (s *composerTestStore) record(call string) (domain.ComposerLease, error) {
	s.calls = append(s.calls, call)
	return s.lease, s.err
}

func (s *composerTestStore) AcquireComposerLease(context.Context, domain.Principal, string, string) (domain.ComposerLease, error) {
	return s.record("acquire")
}

func (s *composerTestStore) RenewComposerLease(_ context.Context, _ domain.Principal, _, _ string, epoch int64, typing bool) (domain.ComposerLease, error) {
	s.epoch, s.typing = epoch, typing
	return s.record("renew")
}

func (s *composerTestStore) ReleaseComposerLease(_ context.Context, _ domain.Principal, _, _ string, epoch int64) (domain.ComposerLease, error) {
	s.epoch = epoch
	return s.record("release")
}

func (s *composerTestStore) RequestComposerTurn(context.Context, domain.Principal, string, string) (domain.ComposerLease, error) {
	return s.record("queue")
}

func (s *composerTestStore) LeaveComposerQueue(context.Context, domain.Principal, string, string) (domain.ComposerLease, error) {
	return s.record("leave")
}

func (s *composerTestStore) TakeOverComposerLease(context.Context, domain.Principal, string, string) (domain.ComposerLease, error) {
	return s.record("takeover")
}

func (s *composerTestStore) SendMessageWithLease(_ context.Context, _ domain.Principal, _, sessionID, _, _ string, _ domain.ChatTurnSettings, epoch int64) (domain.ClientEvent, error) {
	s.sendEpoch = epoch
	return domain.ClientEvent{SessionID: sessionID, Sequence: 1, Type: "chat.user_message", Payload: json.RawMessage(`{}`)}, s.sendErr
}

func (s *composerTestStore) SteerTurn(_ context.Context, _ domain.Principal, _, sessionID, _, _, _ string, epoch int64) (domain.ClientEvent, error) {
	s.steerEpoch = epoch
	return domain.ClientEvent{SessionID: sessionID, Sequence: 2, Type: "chat.turn_steer_requested", Payload: json.RawMessage(`{}`)}, s.sendErr
}

func composerRouter(store Store) http.Handler {
	server := testServer(store)
	router := chi.NewRouter()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), principalKey, domain.Principal{UserID: composerTestUser})))
		})
	})
	router.Route("/orgs/{orgId}/sessions/{sessionId}", func(router chi.Router) {
		router.Post("/messages", server.sendMessage)
		router.Post("/turns/{turnId}/steer", server.steerTurn)
		router.Post("/composer/acquire", server.acquireComposerLease)
		router.Post("/composer/renew", server.renewComposerLease)
		router.Post("/composer/release", server.releaseComposerLease)
		router.Post("/composer/queue", server.requestComposerTurn)
		router.Delete("/composer/queue", server.leaveComposerQueue)
		router.Post("/composer/takeover", server.takeOverComposerLease)
	})
	return router
}

func composerRequest(t *testing.T, handler http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var request *http.Request
	target := "/orgs/" + composerTestOrg + "/sessions/" + composerTestSession + path
	if body == "" {
		request = httptest.NewRequest(method, target, nil)
	} else {
		request = httptest.NewRequest(method, target, strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set("Idempotency-Key", "composer-test-key")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

type composerAnswer struct {
	Composer struct {
		Holder *struct {
			UserID string `json:"userId"`
			Name   string `json:"name"`
		} `json:"holder"`
		Epoch       int64 `json:"epoch"`
		ExpiresInMs int64 `json:"expiresInMs"`
		Waitlist    []struct {
			UserID string `json:"userId"`
			Name   string `json:"name"`
		} `json:"waitlist"`
	} `json:"composer"`
	Granted bool `json:"granted"`
}

func decodeComposerAnswer(t *testing.T, recorder *httptest.ResponseRecorder) composerAnswer {
	t.Helper()
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, body %s", recorder.Code, recorder.Body)
	}
	var answer composerAnswer
	if err := json.Unmarshal(recorder.Body.Bytes(), &answer); err != nil {
		t.Fatalf("decode %s: %v", recorder.Body, err)
	}
	return answer
}

// Each composer route reaches its store operation and answers with the
// lease. The caller is granted only when they hold it, and only then learns
// how long their grant lasts.
func TestComposerRoutesAnswerWithTheLease(t *testing.T) {
	t.Parallel()
	mine := domain.ComposerLease{
		Holder: &domain.ComposerParticipant{UserID: composerTestUser, Name: "Me"}, Epoch: 4,
		ExpiresIn: 12 * time.Second, Waitlist: []domain.ComposerParticipant{{UserID: "user-2", Name: "Bob"}},
	}
	for _, route := range []struct {
		method, path, body, call string
	}{
		{http.MethodPost, "/composer/acquire", "", "acquire"},
		{http.MethodPost, "/composer/renew", `{"epoch":4,"typing":true}`, "renew"},
		{http.MethodPost, "/composer/release", `{"epoch":4}`, "release"},
		{http.MethodPost, "/composer/queue", "", "queue"},
		{http.MethodDelete, "/composer/queue", "", "leave"},
		{http.MethodPost, "/composer/takeover", "", "takeover"},
	} {
		store := &composerTestStore{lease: mine}
		answer := decodeComposerAnswer(t, composerRequest(t, composerRouter(store), route.method, route.path, route.body))
		if len(store.calls) != 1 || store.calls[0] != route.call {
			t.Fatalf("%s %s called %v", route.method, route.path, store.calls)
		}
		if !answer.Granted || answer.Composer.Epoch != 4 || answer.Composer.ExpiresInMs != 12000 ||
			answer.Composer.Holder == nil || answer.Composer.Holder.Name != "Me" ||
			len(answer.Composer.Waitlist) != 1 || answer.Composer.Waitlist[0].Name != "Bob" {
			t.Fatalf("%s %s answer = %+v", route.method, route.path, answer)
		}
	}
}

// A refusal answers 200 with the lease and granted false, without the
// holder's remaining time.
func TestComposerRefusalIsAnAnswerNotAnError(t *testing.T) {
	t.Parallel()
	theirs := domain.ComposerLease{Holder: &domain.ComposerParticipant{UserID: "user-2", Name: "Bob"}, Epoch: 9, ExpiresIn: 10 * time.Second}
	for _, refusal := range []error{postgres.ErrComposerLocked, postgres.ErrComposerLeaseLost} {
		store := &composerTestStore{lease: theirs, err: refusal}
		answer := decodeComposerAnswer(t, composerRequest(t, composerRouter(store), http.MethodPost, "/composer/acquire", ""))
		if answer.Granted || answer.Composer.Holder == nil || answer.Composer.Holder.Name != "Bob" || answer.Composer.ExpiresInMs != 0 {
			t.Fatalf("%v: answer = %+v", refusal, answer)
		}
	}
	// A lease someone else holds is not granted even without an error.
	store := &composerTestStore{lease: theirs}
	if answer := decodeComposerAnswer(t, composerRequest(t, composerRouter(store), http.MethodPost, "/composer/queue", "")); answer.Granted {
		t.Fatalf("queueing behind Bob reported granted: %+v", answer)
	}
}

// Renew and release must name the grant's epoch; other store errors keep
// their usual responses.
func TestComposerRouteErrors(t *testing.T) {
	t.Parallel()
	for _, body := range []string{"", `{}`, `{"epoch":0}`, `{"epoch":-1}`} {
		store := &composerTestStore{}
		recorder := composerRequest(t, composerRouter(store), http.MethodPost, "/composer/renew", body)
		if recorder.Code != http.StatusBadRequest || len(store.calls) != 0 {
			t.Fatalf("renew with %q: status %d, calls %v", body, recorder.Code, store.calls)
		}
	}
	store := &composerTestStore{err: postgres.ErrForbidden}
	if recorder := composerRequest(t, composerRouter(store), http.MethodPost, "/composer/takeover", ""); recorder.Code != http.StatusForbidden {
		t.Fatalf("forbidden take-over status = %d", recorder.Code)
	}
	if recorder := composerRequest(t, composerRouter(&growingEventStore{}), http.MethodPost, "/composer/acquire", ""); recorder.Code != http.StatusNotImplemented {
		t.Fatalf("store without composer support status = %d", recorder.Code)
	}
}

// Sending and steering pass the sender's grant to the store, and a locked
// composer is a 409 the client can recognize.
func TestSendAndSteerCarryTheComposerEpoch(t *testing.T) {
	t.Parallel()
	store := &composerTestStore{}
	handler := composerRouter(store)
	if recorder := composerRequest(t, handler, http.MethodPost, "/messages", `{"text":"hi","composerEpoch":7}`); recorder.Code != http.StatusAccepted || store.sendEpoch != 7 {
		t.Fatalf("send: status %d, epoch %d", recorder.Code, store.sendEpoch)
	}
	turn := "/turns/00000000-0000-4000-8000-0000000000c4/steer"
	if recorder := composerRequest(t, handler, http.MethodPost, turn, `{"text":"stop","composerEpoch":8}`); recorder.Code != http.StatusAccepted || store.steerEpoch != 8 {
		t.Fatalf("steer: status %d, epoch %d", recorder.Code, store.steerEpoch)
	}

	store.sendErr = postgres.ErrComposerLocked
	for _, path := range []string{"/messages", turn} {
		recorder := composerRequest(t, handler, http.MethodPost, path, `{"text":"hi"}`)
		var envelope struct {
			Code string `json:"code"`
		}
		_ = json.Unmarshal(recorder.Body.Bytes(), &envelope)
		if recorder.Code != http.StatusConflict || envelope.Code != "COMPOSER_LOCKED" {
			t.Fatalf("%s while locked: status %d, code %q", path, recorder.Code, envelope.Code)
		}
	}
}

// A live frame carries the lease without the holder's remaining time, so a
// renewal never produces a new frame.
func TestLiveFrameCarriesTheComposer(t *testing.T) {
	t.Parallel()
	frame := toSessionLiveResponse(domain.SessionLiveState{Composer: domain.ComposerLease{
		Holder: &domain.ComposerParticipant{UserID: "user-1", Name: "Alice"}, Epoch: 3, ExpiresIn: 9 * time.Second,
	}})
	payload, err := json.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"type":"session.live","viewers":[],"composer":{"holder":{"userId":"user-1","name":"Alice"},"epoch":3,"waitlist":[]}}`
	if string(payload) != want {
		t.Fatalf("frame = %s\nwant    %s", payload, want)
	}
	free, err := json.Marshal(toSessionLiveResponse(domain.SessionLiveState{}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(free), `"composer":{"holder":null,"epoch":0,"waitlist":[]}`) {
		t.Fatalf("free frame = %s", free)
	}
}
