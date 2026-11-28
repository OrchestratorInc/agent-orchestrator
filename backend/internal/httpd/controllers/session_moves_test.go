package controllers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	sessionsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/session"
)

type importOnlyMover struct {
	SessionMover
	imports int
}

func (m *importOnlyMover) ImportMove(context.Context, sessionsvc.MoveImportInput) (sessionsvc.MoveImportOutcome, error) {
	m.imports++
	return sessionsvc.MoveImportOutcome{}, nil
}

type repeatByte byte

func (b repeatByte) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = byte(b)
	}
	return len(p), nil
}

func TestMoveImportRefusesAnOversizedBody(t *testing.T) {
	mover := &importOnlyMover{}
	r := chi.NewRouter()
	(&SessionMovesController{Svc: mover}).Register(r)
	post := func(body io.Reader) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/sessions/move/import", body))
		return w
	}

	if w := post(strings.NewReader(`{"projectId":"p","moveId":"m"}`)); w.Code != http.StatusOK || mover.imports != 1 {
		t.Fatalf("small body = %d, %d imports; body=%s", w.Code, mover.imports, w.Body)
	}
	oversized := io.MultiReader(
		strings.NewReader(`{"projectId":"p","moveId":"m","transcript":{"transcriptGzip":"`),
		io.LimitReader(repeatByte('A'), maxMoveImportBodyBytes),
		strings.NewReader(`"}}`),
	)
	w := post(oversized)
	if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_JSON") || mover.imports != 1 {
		t.Fatalf("oversized body = %d, %d imports; body=%s", w.Code, mover.imports, w.Body)
	}
}

func TestMoveExportAndCompleteRefuseAnOversizedBody(t *testing.T) {
	r := chi.NewRouter()
	(&SessionMovesController{Svc: &importOnlyMover{}}).Register(r)
	for _, path := range []string{"/sessions/s-1/move/export", "/sessions/s-1/move/complete"} {
		body := io.MultiReader(strings.NewReader(`{"moveId":"`), io.LimitReader(repeatByte('a'), maxMoveRequestBodyBytes), strings.NewReader(`"}`))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodPost, path, body))
		if w.Code != http.StatusBadRequest || !strings.Contains(w.Body.String(), "INVALID_JSON") {
			t.Errorf("%s: %d %s", path, w.Code, w.Body)
		}
	}
}

type targetsMover struct{ SessionMover }

func (targetsMover) MoveTargets(_ context.Context, repoKey, branch string) ([]sessionsvc.MoveTarget, error) {
	return []sessionsvc.MoveTarget{
		{Project: domain.ProjectRecord{ID: "app", DisplayName: "app", Path: "/code/app"}},
		{Project: domain.ProjectRecord{ID: "app-2", DisplayName: "app-2", Path: "/work/app"}, Refusal: apierr.Conflict("MOVE_LOCAL_AHEAD", branch+" here has commits", nil)},
	}, nil
}

func TestMoveTargetsListsProjectsWithTheirRefusals(t *testing.T) {
	r := chi.NewRouter()
	(&SessionMovesController{Svc: targetsMover{}}).Register(r)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/move/targets?repoKey=github.com/acme/app&branch=feat/x", nil))
	var got MoveTargetsResponse
	if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &got) != nil || len(got.Targets) != 2 ||
		got.Targets[0].Refusal != nil || got.Targets[1].Refusal == nil || got.Targets[1].Refusal.Code != "MOVE_LOCAL_AHEAD" {
		t.Fatalf("targets = %d %s", w.Code, w.Body)
	}
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/sessions/move/targets?repoKey=github.com/acme/app", nil))
	if w.Code != http.StatusBadRequest {
		t.Fatalf("missing branch = %d, want 400", w.Code)
	}
}
