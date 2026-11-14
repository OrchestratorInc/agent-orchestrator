package controllers

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"

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
