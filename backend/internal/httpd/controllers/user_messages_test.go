package controllers_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type userMessageCall struct {
	id      domain.SessionID
	isSteer bool
}

type fakeUserMessages struct{ calls []userMessageCall }

func (f *fakeUserMessages) RecordUserMessage(_ context.Context, id domain.SessionID, isSteer bool) {
	f.calls = append(f.calls, userMessageCall{id: id, isSteer: isSteer})
}

func postUserMessage(t *testing.T, svc *steerStub, recorder *fakeUserMessages, path string, body any) int {
	t.Helper()
	r := chi.NewRouter()
	r.Route("/api/v1", (&controllers.ConversationsController{Svc: svc, UserMessages: recorder}).Register)
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/v1/sessions/p1-1/conversation/"+path, bytes.NewReader(encoded)))
	return rec.Code
}

// Only a person's accepted steer is counted here: an AO session steering
// another (`ao send --steer` inside a session), an idempotent replay, and a
// send that became a Chat message (counted at its insert) are not.
func TestUserMessageRecordedOnlyForHumanSends(t *testing.T) {
	cases := []struct {
		name   string
		path   string
		result chatsvc.SteerOrSendResult
		body   map[string]any
		want   []userMessageCall
	}{
		{"steer", "steer", chatsvc.SteerOrSendResult{}, map[string]any{"text": "focus", "clientMessageId": "a"},
			[]userMessageCall{{id: "p1-1", isSteer: true}}},
		{"agent steer", "steer", chatsvc.SteerOrSendResult{}, map[string]any{"text": "focus", "clientMessageId": "a", "senderSessionId": "orch-1"}, nil},
		{"steer-or-send that steered", "steer-or-send", chatsvc.SteerOrSendResult{Steered: true}, map[string]any{"text": "x", "clientMessageId": "b"},
			[]userMessageCall{{id: "p1-1", isSteer: true}}},
		// A fallback send is a Chat message, counted where it is inserted.
		{"steer-or-send that sent", "steer-or-send", chatsvc.SteerOrSendResult{}, map[string]any{"text": "x", "clientMessageId": "c"}, nil},
		{"replayed steer-or-send", "steer-or-send", chatsvc.SteerOrSendResult{Duplicate: true}, map[string]any{"text": "x", "clientMessageId": "d"}, nil},
		{"recover only", "steer-or-send", chatsvc.SteerOrSendResult{}, map[string]any{"clientMessageId": "e", "recoverOnly": true}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc := &steerStub{fakeConversationService: &fakeConversationService{}, atomicResult: tc.result}
			recorder := &fakeUserMessages{}
			if code := postUserMessage(t, svc, recorder, tc.path, tc.body); code != http.StatusAccepted {
				t.Fatalf("status = %d", code)
			}
			if len(recorder.calls) != len(tc.want) {
				t.Fatalf("calls = %+v, want %+v", recorder.calls, tc.want)
			}
			for i := range tc.want {
				if recorder.calls[i] != tc.want[i] {
					t.Fatalf("call %d = %+v, want %+v", i, recorder.calls[i], tc.want[i])
				}
			}
		})
	}
}
