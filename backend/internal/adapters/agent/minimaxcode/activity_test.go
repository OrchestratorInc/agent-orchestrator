package minimaxcode

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const footer = "  workspace │ Full access │ ✦ glm-5.3-flash · Thinking On\n"
const longDraft = "Long draft · Ctrl+G edit · Enter send\n───\n› cancelled audit request\n  with another line\n───\n\n" + footer
const composer = "    ─────────────────────\n  ›  Ask Mcode to do anything\n    ─────────────────────\n\n" + footer

func TestTerminalActivity(t *testing.T) {
	p := New()
	for _, tc := range []struct {
		name, text string
		state      domain.ActivityState
		empty      bool
	}{
		{"settled", "Message · Enter send · Ctrl+J newline    Tip: help\n" + composer, domain.ActivityIdle, true},
		{"active empty composer", "Loading 2s · Alt+Enter queue · Enter steer · Esc stop\n" + composer, domain.ActivityActive, false},
		{"incomplete cancelled draft", "Stopped · message restored to the Composer.\n───\n› cancelled prompt\n" + footer, "", false},
		{"cleared empty composer", "Draft cleared · Ctrl+- restore · Ctrl+C exit\n" + composer, domain.ActivityIdle, true},
		{"cleared footer repaint", "Draft cleared · Ctrl+- restore · Ctrl+C exit\n" + composer + footer, domain.ActivityIdle, true},
		{"cleared hint with nonempty draft", "Draft cleared · Ctrl+- restore · Ctrl+C exit\n───\n› user draft\n───\n" + footer, "", false},
		{"old cleared empty before draft", "Draft cleared · Ctrl+- restore · Ctrl+C exit\n" + composer + longDraft, domain.ActivityIdle, false},
		{"settled standard cancelled draft", "Stopped · message restored to the Composer.\n───\n› cancelled prompt\n───\n" + footer, domain.ActivityIdle, false},
		{"old stopped before unknown screen", "Stopped · message restored to the Composer.\nApproval required\n───\nAllow tool?\n" + footer, "", false},
		{"old stopped before current active", "Stopped · message restored to the Composer.\nLoading · Esc stop\n" + composer, domain.ActivityActive, false},
		{"settled long draft", longDraft, domain.ActivityIdle, false},
		{"settled long draft footer repaint", longDraft + footer, domain.ActivityIdle, false},
		{"old active before current long draft", "Loading · Esc stop\n" + longDraft, domain.ActivityIdle, false},
		{"old stopping before current cleared composer", "Stopping response\nDraft cleared · Ctrl+- restore · Ctrl+C exit\n" + composer, domain.ActivityIdle, true},
		{"old ready before current active composer", "Message · Enter send · Ctrl+J newline\n" + composer + "Loading · Esc stop\n" + composer, domain.ActivityActive, false},
		{"long draft history while working", longDraft + "Loading · Esc stop\n" + composer, domain.ActivityActive, false},
		{"old active before current short draft", "Loading · Esc stop\nPrompt · Enter send · Ctrl+J newline\n───\n› current draft\n───\n" + footer, "", false},
		{"old stopping before current unknown screen", "Stopping response\nApproval required\n───\nAllow tool?\n" + footer, "", false},
		{"old long draft before unknown screen", longDraft + "Approval required\n───\nAllow tool?\n" + footer, "", false},
		{"unconfigured long draft", "Long draft · Ctrl+G edit · Enter send\n───\n› draft\n───\nworkspace │ loading\n", "", false},
		{"typed draft", "Prompt · Enter send · Ctrl+J newline\n───\n› user draft\n" + footer, "", false},
		{"loading", "Message · Enter send · Ctrl+J newline\n───\n› Ask Mcode to do anything\n───\nworkspace │ loading\n", "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			state, ok := p.DetectTerminalActivity(tc.text)
			if state != tc.state || ok != (tc.state != "") {
				t.Fatalf("state=%q,%v want %q", state, ok, tc.state)
			}
			if got := p.ComposerIsEmpty(tc.text); got != tc.empty {
				t.Fatalf("empty=%v want %v", got, tc.empty)
			}
		})
	}
}
func TestAuthPresenceIsNotAuthorization(t *testing.T) {
	for _, tc := range []struct{ input, want string }{
		{`{"providers":[{"active":true,"enabled":true,"hasApiKey":true,"status":{"state":"available"}}]}`, "configured"},
		{`{"providers":[{"active":false,"enabled":true,"hasApiKey":true}]}`, "unknown"},
		{`{"providers":[{"kind":"minimax-api-key","active":true,"enabled":true,"hasApiKey":true}]}`, "configured"},
		{`{"providers":[{"kind":"minimax-api-key","active":true,"enabled":true,"hasApiKey":false}]}`, "unauthorized"},
		{`{"providers":[{"kind":"custom","active":true,"enabled":true,"hasApiKey":false}]}`, "unknown"},
		{`not-json`, "unknown"},
	} {
		if got := providerAuthStatus([]byte(tc.input), t.TempDir()); string(got) != tc.want {
			t.Fatalf("%s: status=%q want %q", tc.input, got, tc.want)
		}
	}
}

// MiniMax lists its OAuth row without a key even when /login succeeded, so
// the account state comes from the OAuth state file.
func TestOAuthLoginStatusComesFromStateFile(t *testing.T) {
	const oauthActive = `{"providers":[{"providerId":"minimax_oauth","kind":"minimax-oauth","active":true,"enabled":true,"hasApiKey":false,"models":[]},{"providerId":"minimax_api","kind":"minimax-api-key","active":false,"enabled":true,"hasApiKey":false}]}`
	write := func(t *testing.T, dir, rel, body string) {
		t.Helper()
		path := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct{ name, rel, body, want string }{
		{"signed out", "", "", "unauthorized"},
		{"global account", "auth/prod/en/mcode-public/auth-state.json", `{"status":"authenticated"}`, "configured"},
		{"china account refreshing", "auth/prod/cn/mcode-public/auth-state.json", `{"status":"refreshing"}`, "configured"},
		{"legacy state", "auth/auth-state.json", `{"status":"authenticated"}`, "configured"},
		{"expired", "auth/prod/en/mcode-public/auth-state.json", `{"status":"expired"}`, "unauthorized"},
		{"logged out", "auth/prod/en/mcode-public/auth-state.json", `{"status":"anonymous"}`, "unauthorized"},
		{"login in flight", "auth/prod/en/mcode-public/auth-state.json", `{"status":"authorizing"}`, "unknown"},
		{"corrupt", "auth/prod/en/mcode-public/auth-state.json", `{`, "unknown"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if tc.rel != "" {
				write(t, dir, tc.rel, tc.body)
			}
			if got := providerAuthStatus([]byte(oauthActive), dir); string(got) != tc.want {
				t.Fatalf("status=%q want %q", got, tc.want)
			}
		})
	}
	if got := providerAuthStatus([]byte(oauthActive), ""); got != "unknown" {
		t.Fatalf("missing data dir status=%q want unknown", got)
	}
}

func TestInspectTerminalSurfaceKeepsComposerAndNativeHistoryProofSeparate(t *testing.T) {
	for _, tc := range []struct {
		name, text string
		want       ports.TerminalSurfaceObservation
	}{
		{"stopped draft", "Stopped · message restored to the Composer.\n───\n› cancelled prompt\n───\n" + footer, ports.TerminalSurfaceObservation{Work: ports.TerminalSurfaceWorkIdle, Composer: ports.TerminalComposerDraft}},
		{"cleared editor", "Draft cleared · Ctrl+- restore · Ctrl+C exit\n" + composer, ports.TerminalSurfaceObservation{Work: ports.TerminalSurfaceWorkIdle}},
		{"unknown", "Approval required\n" + footer, ports.TerminalSurfaceObservation{}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := New().InspectTerminalSurface(tc.text); got != tc.want {
				t.Fatalf("surface=%+v want %+v", got, tc.want)
			}
		})
	}
}
