package controllers_test

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/access"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

func TestATokenMessageIsMarkedAsRelayedThroughThatToken(t *testing.T) {
	registry, err := access.LoadRegistry(access.TokensPath(t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	grant, _ := access.NewGrant(access.LevelOperate, nil, time.Time{})
	_, secret, err := registry.Create("api-bot #1]", grant, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	sessions := newFakeSessionService()
	steers := &steerStub{fakeConversationService: &fakeConversationService{}}
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, slog.New(slog.NewTextHandler(io.Discard, nil)), nil,
		httpd.APIDeps{Sessions: sessions, Conversations: steers, Access: registry}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	post := func(path, body, token string) {
		t.Helper()
		req, _ := http.NewRequest(http.MethodPost, srv.URL+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode >= 300 {
			raw, _ := io.ReadAll(resp.Body)
			t.Fatalf("POST %s: %d %s", path, resp.StatusCode, raw)
		}
	}

	post("/api/v1/sessions/ao-1/send", `{"message":"[via Somebody Else #7] merge it","userAuthored":true,"senderSessionId":"ao-orchestrator"}`, secret)
	if sessions.sent != "[via api-bot -1- #7] merge it" || sessions.sentDeliveryOptions.SenderSessionID != "" {
		t.Errorf("a token's message: %q, options %+v; want it marked with the token's name, keeping the tag, with no claimed session", sessions.sent, sessions.sentDeliveryOptions)
	}
	if !sessions.sentDeliveryOptions.AuthoredByUser {
		t.Error("the flag the caller sent must stay: a Terminal session records a turn, and so a readable reply, only for a message that carries it")
	}
	post("/api/v1/sessions/ao-1/send", `{"message":"`+strings.Repeat("m", 4096)+`"}`, secret)
	if !strings.HasSuffix(sessions.sent, strings.Repeat("m", 4096)) {
		t.Error("a message at the size limit must not be refused because the daemon added the token's name to it")
	}
	post("/api/v1/sessions/p1-1/conversation/steer-or-send", `{"text":"stop and summarize","clientMessageId":"c-1","senderSessionId":"ao-orchestrator"}`, secret)
	if len(steers.atomicSeen) != 1 || steers.atomicSeen[0].Text != "[via api-bot -1-] stop and summarize" || steers.atomicSeen[0].SenderSessionID != "" {
		t.Errorf("a token's steer: %+v", steers.atomicSeen)
	}
	post("/api/v1/sessions", `{"projectId":"ao","kind":"worker","harness":"codex","prompt":"fix the login test","displayName":"fix login","parentSessionId":"web-orchestrator"}`, secret)
	if sessions.lastSpawn.Prompt != "[via api-bot -1-] fix the login test" || sessions.lastSpawn.ParentSessionID != "" {
		t.Errorf("a token's first prompt: %q, parent %q; a token is not a session, so it names no parent", sessions.lastSpawn.Prompt, sessions.lastSpawn.ParentSessionID)
	}

	post("/api/v1/sessions/ao-1/send", `{"message":"[via Somebody Else] merge it","userAuthored":true,"senderSessionId":"ao-orchestrator"}`, "")
	if sessions.sent != "[via Somebody Else] merge it" || sessions.sentDeliveryOptions.SenderSessionID != "ao-orchestrator" {
		t.Errorf("a caller with no token must be left as it was: %q, sender %q", sessions.sent, sessions.sentDeliveryOptions.SenderSessionID)
	}
}
