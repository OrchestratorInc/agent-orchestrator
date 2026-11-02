package codexappserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// These fixtures use the native account/rateLimits/updated and TurnError shapes
// from Codex's app-server schema. They do not contact a live provider or exhaust
// quota. Display-only dates intentionally lack a timezone and remain unusable.
const codexQuotaCopy = "You've hit your usage limit. Try again at Aug 2nd, 2026 3:31 PM."

func TestCodexUsageLimitNativeErrors(t *testing.T) {
	for _, tc := range []struct {
		name    string
		reason  string
		message string
		quota   bool
		auth    bool
	}{
		{"typed quota with generic copy", `"usageLimitExceeded"`, "Please try again later.", true, false},
		{"typed quota without copy", `"usageLimitExceeded"`, "", true, false},
		{"typed quota with display date", `"usageLimitExceeded"`, codexQuotaCopy, true, false},
		{"throttling is not quota", `"rateLimitExceeded"`, "Too many requests.", false, false},
		{"context exhaustion is not quota", `"contextWindowExceeded"`, "Context window exceeded.", false, false},
		{"session budget is not quota", `"sessionBudgetExceeded"`, "Session budget exceeded.", false, false},
		{"unauthorized keeps authentication path", `"unauthorized"`, "Please sign in.", false, true},
		{"transport status is not quota", `{"httpConnectionFailed":{"httpStatusCode":429}}`, "Request failed.", false, false},
		{"untyped generic copy is not quota", `null`, "Please try again later.", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			failure := fmt.Sprintf(`{"message":%q,"codexErrorInfo":%s}`, tc.message, tc.reason)
			for _, method := range []string{"error", "turn/completed"} {
				params := `{"threadId":"thread-1","turnId":"turn-1","willRetry":false,"error":` + failure + `}`
				if method == "turn/completed" {
					params = `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":` + failure + `}}`
				}
				ev := normalizeOne(t, method, params)
				if ev.Err == nil || errors.Is(ev.Err, ports.ErrChatAuthRequired) != tc.auth {
					t.Fatalf("%s failure/auth = %v", method, ev.Err)
				}
				limit := domain.ParseConversationUsageLimit(ev.Err.Error(), nil)
				if (limit != nil) != tc.quota || (limit != nil && limit.ResetsAt != nil) {
					t.Fatalf("%s limit = %#v, error = %v", method, limit, ev.Err)
				}
				if !strings.Contains(ev.Err.Error(), tc.message) {
					t.Fatalf("%s discarded provider copy: %v", method, ev.Err)
				}
			}
		})
	}
}

func TestCodexUsageLimitResetWindows(t *testing.T) {
	now := time.Date(2026, 8, 2, 10, 0, 0, 0, time.UTC)
	const firstReset = int64(1785664860)
	const secondReset = int64(1785664920)
	for _, tc := range []struct {
		name   string
		bucket string
		reset  int64
	}{
		{"primary saturated", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":null}`, firstReset},
		{"secondary saturated", `{"limitId":"codex","primary":{"usedPercent":20},"secondary":{"usedPercent":100,"resetsAt":1785664920}}`, secondReset},
		{"both saturated waits for latest", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":{"usedPercent":100,"resetsAt":1785664920}}`, secondReset},
		{"explicit rolling refusal", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":null,"rateLimitReachedType":"rate_limit_reached"}`, firstReset},
		{"other saturated window missing reset", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":{"usedPercent":100}}`, 0},
		{"elapsed reset", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664800},"secondary":null}`, 0},
		{"missing saturation", `{"limitId":"codex","primary":{"resetsAt":1785664860},"secondary":null}`, 0},
		{"not saturated", `{"limitId":"codex","primary":{"usedPercent":99,"resetsAt":1785664860},"secondary":null}`, 0},
		{"invalid percent", `{"limitId":"codex","primary":{"usedPercent":101,"resetsAt":1785664860},"secondary":null}`, 0},
		{"sparse update omits secondary", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860}}`, 0},
		{"sparse update omits primary", `{"limitId":"codex","secondary":{"usedPercent":100,"resetsAt":1785664860}}`, 0},
		{"unidentified bucket", `{"primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":null}`, 0},
		{"model specific bucket", `{"limitId":"codex_other_model","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":null}`, 0},
		{"spend control has no safe rolling deadline", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":null,"spendControlReached":true}`, 0},
		{"credits depleted has no safe rolling deadline", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":null,"rateLimitReachedType":"workspace_owner_credits_depleted"}`, 0},
		{"member spend cap has no safe rolling deadline", `{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":1785664860},"secondary":null,"rateLimitReachedType":"workspace_member_usage_limit_reached"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var tracker usageLimitTracker
			tracker.observe(usageLimitNotification("turn/started", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"inProgress"}}`), "thread-1", now)
			tracker.observe(usageLimitNotification("account/rateLimits/updated", `{"rateLimits":`+tc.bucket+`}`), "thread-1", now)
			ev := normalizeOne(t, "turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"message":"`+codexQuotaCopy+`","codexErrorInfo":"usageLimitExceeded"}}}`)
			ev = tracker.decorate(ev, now)
			assertCodexQuotaReset(t, ev, tc.reset)
			if tracker.turnID != "" || !tracker.resetAt.IsZero() {
				t.Fatalf("completed turn retained quota metadata: %#v", tracker)
			}
			if tc.name == "primary saturated" {
				want := "Usage limit exceeded; resets 2026-08-02T10:01:00Z\n\n" + codexQuotaCopy
				if ev.Err.Error() != want {
					t.Fatalf("durable copy = %q, want %q", ev.Err, want)
				}
			}
		})
	}
}

func TestCodexUsageLimitNotificationOrdering(t *testing.T) {
	const reset = int64(4102444800) // 2100-01-01T00:00:00Z, safely future for pump's real clock.
	const start = `{"method":"turn/started","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"inProgress"}}}`
	const window = `{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":4102444800},"secondary":null}}}`
	const sparse = `{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex","primary":{"usedPercent":100,"resetsAt":4102444800}}}}`
	const otherBucket = `{"method":"account/rateLimits/updated","params":{"rateLimits":{"limitId":"codex_other_model","primary":{"usedPercent":100,"resetsAt":4102444800},"secondary":null}}}`
	const failure = `{"method":"error","params":{"threadId":"thread-1","turnId":"turn-1","willRetry":false,"error":{"message":"` + codexQuotaCopy + `","codexErrorInfo":"usageLimitExceeded"}}}`
	const completed = `{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed"}}}`
	const failedWithError = `{"method":"turn/completed","params":{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"message":"` + codexQuotaCopy + `","codexErrorInfo":"usageLimitExceeded"}}}}`
	const childFailure = `{"method":"error","params":{"threadId":"child-thread","turnId":"turn-1","error":{"message":"` + codexQuotaCopy + `","codexErrorInfo":"usageLimitExceeded"}}}`
	for _, tc := range []struct {
		name   string
		frames []string
		resets []int64
	}{
		{"terminal error survives completion without error", []string{start, window, failure, completed}, []int64{reset, reset}},
		{"error only on completion", []string{start, window, failedWithError}, []int64{reset}},
		{"error and completion both retain reset", []string{start, window, failure, failedWithError}, []int64{reset, reset}},
		{"pre-turn account cache is not reused", []string{window, start, failure, completed}, []int64{0, 0}},
		{"missing turn start stays unknown", []string{window, failure, completed}, []int64{0, 0}},
		{"late account observation does not rewrite terminal error", []string{start, failure, window, completed}, []int64{0, 0}},
		{"new turn clears old observation", []string{start, window, failure, completed, start, failure, completed}, []int64{reset, reset, 0, 0}},
		{"child failure cannot borrow root deadline", []string{start, window, childFailure, failure, completed}, []int64{0, reset, reset}},
		{"other bucket invalidates earlier codex observation", []string{start, window, otherBucket, failure, completed}, []int64{0, 0}},
		{"later codex update cannot resolve bucket ambiguity", []string{start, otherBucket, window, failure, completed}, []int64{0, 0}},
		{"partial update invalidates earlier complete observation", []string{start, window, sparse, failure, completed}, []int64{0, 0}},
		{"malformed update invalidates earlier complete observation", []string{start, window, `{"method":"account/rateLimits/updated","params":{"rateLimits":{"primary":"invalid"}}}`, failure, completed}, []int64{0, 0}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conv, srv := openProviderFailureConversation(t)
			for _, frame := range tc.frames {
				srv.push(frame)
			}
			srv.push(`{"method":"thread/name/updated","params":{"threadId":"thread-1","threadName":"sentinel"}}`)
			var seen int
			deadline := time.After(5 * time.Second)
			for {
				select {
				case ev := <-conv.Events():
					if ev.Kind == ports.ChatEventTurnCompleted && ev.TurnState == domain.TurnStateFailed && ev.Err == nil {
						t.Fatalf("failed completion lost the durable quota refusal: %#v", ev)
					}
					if ev.Err != nil {
						if seen >= len(tc.resets) {
							t.Fatalf("unexpected extra failure: %v", ev.Err)
						}
						assertCodexQuotaReset(t, ev, tc.resets[seen])
						seen++
					}
					if ev.Kind == ports.ChatEventThreadRenamed {
						if seen != len(tc.resets) {
							t.Fatalf("received %d failures, want %d", seen, len(tc.resets))
						}
						return
					}
				case <-deadline:
					t.Fatal("timed out awaiting quota sequence")
				}
			}
		})
	}
}

func TestCodexUsageLimitTerminalFallbackSafety(t *testing.T) {
	now := testNow
	const quota = `{"threadId":"thread-1","turnId":"turn-1","error":{"message":"` + codexQuotaCopy + `","codexErrorInfo":"usageLimitExceeded"}}`
	for _, tc := range []struct {
		name       string
		completion string
		intervene  string
		wantError  string
	}{
		{"successful completion does not inherit quota", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"completed"}}`, "", ""},
		{"interrupted completion does not inherit quota", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"interrupted"}}`, "", ""},
		{"another turn cannot inherit quota", `{"threadId":"thread-1","turn":{"id":"turn-2","status":"failed"}}`, "", ""},
		{"child completion with same ID cannot inherit quota", `{"threadId":"child-thread","turn":{"id":"turn-1","status":"failed"}}`, "", ""},
		{"attached failure remains authoritative", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"message":"Network failed."}}}`, "", "Network failed."},
		{"later unrelated error clears quota fallback", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed"}}`, `{"threadId":"thread-1","turnId":"turn-1","error":{"message":"Network failed."}}`, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := usageLimitTracker{threadID: "thread-1", turnID: "turn-1"}
			tracker.decorate(normalizeOne(t, "error", quota), now)
			if tc.intervene != "" {
				tracker.decorate(normalizeOne(t, "error", tc.intervene), now)
			}
			ev := tracker.decorate(normalizeOne(t, "turn/completed", tc.completion), now)
			message := ""
			if ev.Err != nil {
				message = ev.Err.Error()
			}
			if message != tc.wantError {
				t.Fatalf("completion error = %q, want %q", message, tc.wantError)
			}
			if ev.ProviderConversationID != "thread-1" || ev.ProviderTurnID != "turn-1" {
				root := tracker.decorate(normalizeOne(t, "turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed"}}`), now)
				assertCodexQuotaReset(t, root, 0)
			}
		})
	}
}

func TestCodexUsageLimitHistoricalTypedErrorKeepsUnknownReset(t *testing.T) {
	conv, srv := openProviderFailureConversation(t)
	srv.reply("thread/read", `{"thread":{"id":"thread-1","turns":[{"id":"turn-a","status":"failed","error":{"message":"Try again later.","codexErrorInfo":"usageLimitExceeded"}}]}}`)
	events, err := conv.ReadHistory(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[1].Err == nil || events[1].TurnState != domain.TurnStateFailed {
		t.Fatalf("historical quota outcome = %#v", events)
	}
	limit := domain.ParseConversationUsageLimit(events[1].Err.Error(), nil)
	if limit == nil || limit.ResetsAt != nil || !strings.Contains(events[1].Err.Error(), "Try again later.") {
		t.Fatalf("historical quota invented reset or lost copy: %#v, %v", limit, events[1].Err)
	}
}

func TestCodexUsageLimitMetadataDoesNotReclassifyOtherErrors(t *testing.T) {
	now := testNow
	tracker := usageLimitTracker{threadID: "thread-1", turnID: "turn-1", resetAt: now.Add(time.Hour)}
	ev := normalizeOne(t, "turn/completed", `{"threadId":"thread-1","turn":{"id":"turn-1","status":"failed","error":{"message":"Too many requests.","codexErrorInfo":"rateLimitExceeded"}}}`)
	ev = tracker.decorate(ev, now)
	if ev.Err == nil || ev.Err.Error() != "Too many requests." || domain.ParseConversationUsageLimit(ev.Err.Error(), nil) != nil {
		t.Fatalf("native quota metadata reclassified throttling: %v", ev.Err)
	}
}

func usageLimitNotification(method, params string) notification {
	return notification{Method: method, Params: json.RawMessage(params)}
}

func assertCodexQuotaReset(t *testing.T, ev ports.ChatEvent, reset int64) {
	t.Helper()
	if ev.Err == nil || !strings.Contains(ev.Err.Error(), codexQuotaCopy) {
		t.Fatalf("quota failure lost provider explanation: %#v", ev)
	}
	limit := domain.ParseConversationUsageLimit(ev.Err.Error(), nil)
	if limit == nil || (reset == 0 && limit.ResetsAt != nil) ||
		(reset != 0 && (limit.ResetsAt == nil || limit.ResetsAt.Unix() != reset)) {
		t.Fatalf("quota reset = %#v, want Unix %d; error = %v", limit, reset, ev.Err)
	}
}
