package usage

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func cursorTestToken(sub string) string {
	payload := base64.RawURLEncoding.EncodeToString([]byte(fmt.Sprintf(`{"sub":%q}`, sub)))
	return "header." + payload + ".signature"
}

func cursorTestEvent(index int) map[string]any {
	return map[string]any{
		"timestamp":      fmt.Sprint(1_000_000 + index),
		"model":          "gpt-5",
		"conversationId": fmt.Sprintf("conversation-%d", index%3),
		"tokenUsage":     map[string]any{"inputTokens": 1, "outputTokens": 1, "totalCents": 0.1},
	}
}

func TestReadCursorDashboardReconcilesPagesAndMapsEvents(t *testing.T) {
	token := cursorTestToken("auth0|user_123")
	// 1,003 events. Page two repeats page one's last event, which the count
	// of 1,003 reveals; event 5 billed no tokens.
	events := make([]map[string]any, 1003)
	for index := range events {
		events[index] = cursorTestEvent(index)
	}
	events[5]["tokenUsage"] = nil
	events[7] = map[string]any{
		"timestamp": 1_000_007, "model": "claude-4.5-sonnet", "conversationId": "chat",
		"tokenUsage": map[string]any{"inputTokens": 10, "cacheReadTokens": 20, "cacheWriteTokens": 5, "outputTokens": 7, "totalCents": 1.5},
	}
	pages := map[int][]map[string]any{1: events[:1000], 2: events[999:]}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Page      int    `json:"page"`
			PageSize  int    `json:"pageSize"`
			StartDate string `json:"startDate"`
			EndDate   string `json:"endDate"`
		}
		wantCookie := "WorkosCursorSessionToken=" + url.QueryEscape("user_123::"+token)
		if r.Method != http.MethodPost || r.Header.Get("Origin") != "https://cursor.com" || r.Header.Get("Cookie") != wantCookie ||
			json.NewDecoder(r.Body).Decode(&body) != nil || body.PageSize != 1000 || body.StartDate != "1000000" || body.EndDate != "1002002" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"totalUsageEventsCount": 1003, "usageEventsDisplay": pages[body.Page]})
	}))
	defer server.Close()

	got, err := ReadCursorDashboard(context.Background(), server.Client(), server.URL, token,
		time.UnixMilli(1_000_000), time.UnixMilli(1_002_002))
	if err != nil {
		t.Fatalf("ReadCursorDashboard: %v", err)
	}
	if len(got) != 1002 {
		t.Fatalf("read %d events, want 1002 (1,003 less the one with no tokens)", len(got))
	}
	keys := map[string]bool{}
	for _, event := range got {
		if keys[event.Event.SourceEventKey] {
			t.Fatalf("duplicate event key %q", event.Event.SourceEventKey)
		}
		keys[event.Event.SourceEventKey] = true
	}

	claude := got[6] // events 0-4, then 6, then 7
	tokens := claude.Event.Tokens
	if claude.ConversationID != "chat" || claude.Event.ProviderID != domain.UsageProviderAnthropic ||
		claude.Event.BillingProviderID != "cursor" || !claude.Event.CreatedAt.Equal(time.UnixMilli(1_000_007)) ||
		*tokens.InputTokens != 35 || *tokens.CachedInputTokens != 20 || *tokens.UncachedInputTokens != 15 || *tokens.OutputTokens != 7 ||
		claude.Event.Costs.EstimatedCostNanos == nil || *claude.Event.Costs.EstimatedCostNanos != 15_000_000 ||
		claude.Event.Costs.PricingVersion != "cursor:reported" {
		t.Fatalf("claude event = %+v, tokens %+v", claude, tokens)
	}
}

func TestReadCursorDashboardRefusesSignedOutAndRedirects(t *testing.T) {
	token := cursorTestToken("user_1")
	window := func(server *httptest.Server) error {
		_, err := ReadCursorDashboard(context.Background(), server.Client(), server.URL, token, time.UnixMilli(0), time.UnixMilli(1))
		return err
	}

	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer refused.Close()
	if err := window(refused); !errors.Is(err, ErrCursorSignedOut) {
		t.Fatalf("401: err = %v, want ErrCursorSignedOut", err)
	}

	followed := false
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		followed = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer elsewhere.Close()
	redirecting := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, elsewhere.URL, http.StatusTemporaryRedirect)
	}))
	defer redirecting.Close()
	if err := window(redirecting); err == nil || followed {
		t.Fatalf("redirect: err = %v, followed = %v; want an error and no request elsewhere", err, followed)
	}

	if _, err := ReadCursorDashboard(context.Background(), http.DefaultClient, refused.URL, "not-a-jwt", time.UnixMilli(0), time.UnixMilli(1)); !errors.Is(err, ErrCursorSignedOut) {
		t.Fatalf("bad token: err = %v, want ErrCursorSignedOut", err)
	}
}

func TestCursorBoundaryOverlap(t *testing.T) {
	for _, tc := range []struct {
		previous, current []string
		want              int
	}{
		{[]string{"a", "b", "c"}, []string{"b", "c", "d"}, 2},
		{[]string{"a", "b"}, []string{"c"}, 0},
		{nil, []string{"a"}, 0},
		{[]string{"a"}, nil, 0},
		{[]string{"a", "a"}, []string{"a", "a", "a"}, 2},
	} {
		if got := cursorBoundaryOverlap(tc.previous, tc.current); got != tc.want {
			t.Errorf("overlap(%v, %v) = %d, want %d", tc.previous, tc.current, got, tc.want)
		}
	}
}
