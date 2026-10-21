package controllers

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	awakesvc "github.com/aoagents/agent-orchestrator/backend/internal/service/awake"
)

type awakeHTTPStub struct {
	AwakeService
	starts int
	err    error
}

func (s *awakeHTTPStub) Status() domain.AwakeState {
	return domain.AwakeState{Sessions: []domain.AwakeSession{}}
}
func (s *awakeHTTPStub) Start(context.Context, domain.StartAwakeRequest) error {
	s.starts++
	return s.err
}
func TestAwakeRejectsMalformedRequestsBeforeMutation(t *testing.T) {
	for _, body := range []string{`{`, `{"unknown":true}`, `{"mode":"indefinite"} {}`, strings.Repeat(" ", 65537) + `{}`} {
		t.Run(body[:min(len(body), 30)], func(t *testing.T) {
			svc := &awakeHTTPStub{}
			c := AwakeController{Svc: svc}
			w := httptest.NewRecorder()
			c.Start(w, httptest.NewRequest(http.MethodPost, "/api/v1/awake/sessions", strings.NewReader(body)))
			if w.Code != 400 || svc.starts != 0 {
				t.Fatalf("status=%d mutations=%d", w.Code, svc.starts)
			}
		})
	}
}
func TestAwakeHTTPValidationAndRuntimeErrors(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, 200}, {awakesvc.ErrValidation, 400}, {errors.New("journal unavailable"), 500}} {
		svc := &awakeHTTPStub{err: tc.err}
		w := httptest.NewRecorder()
		(&AwakeController{Svc: svc}).Start(w, httptest.NewRequest(http.MethodPost, "/api/v1/awake/sessions", strings.NewReader(`{"mode":"indefinite"}`)))
		if w.Code != tc.status || svc.starts != 1 {
			t.Fatalf("status=%d mutations=%d", w.Code, svc.starts)
		}
	}
	w := httptest.NewRecorder()
	(&AwakeController{}).Status(w, httptest.NewRequest(http.MethodGet, "/api/v1/awake", nil))
	if w.Code != 501 {
		t.Fatalf("missing service status=%d", w.Code)
	}
}

func (s *awakeHTTPStub) AuthorizeLowPower(context.Context) error { s.starts++; return s.err }
func TestAwakeLowPowerAuthorizationResult(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, http.StatusOK}, {errors.New("authorization canceled"), http.StatusInternalServerError}} {
		svc := &awakeHTTPStub{err: tc.err}
		w := httptest.NewRecorder()
		(&AwakeController{Svc: svc}).AuthorizeLowPower(w, httptest.NewRequest(http.MethodPost, "/api/v1/awake/low-power/authorize", nil))
		if w.Code != tc.status || svc.starts != 1 {
			t.Fatalf("status=%d calls=%d", w.Code, svc.starts)
		}
	}
}

func (s *awakeHTTPStub) RevokeLowPower(context.Context) error { s.starts++; return s.err }
func TestAwakeLowPowerRevocationResult(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
	}{{nil, http.StatusOK}, {awakesvc.ErrValidation, http.StatusBadRequest}, {errors.New("revocation canceled"), http.StatusInternalServerError}} {
		svc := &awakeHTTPStub{err: tc.err}
		w := httptest.NewRecorder()
		(&AwakeController{Svc: svc}).RevokeLowPower(w, httptest.NewRequest(http.MethodPost, "/api/v1/awake/low-power/revoke", nil))
		if w.Code != tc.status || svc.starts != 1 {
			t.Fatalf("status=%d calls=%d", w.Code, svc.starts)
		}
	}
}
