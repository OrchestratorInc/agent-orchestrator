package httpapi

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/cloud/internal/domain"
)

// The renderer derives a cloud session's display name from the task brief with a
// 100-character slice (frontend TaskComposer). validSessionInput must accept the
// full width of that slice, or cloud task creation 422s with "Session ... is
// invalid" for any brief longer than the cap. Regression guard for the 80 -> 100
// mismatch introduced by #5125.
func TestValidSessionInputDisplayNameLength(t *testing.T) {
	base := createSessionRequest{
		ProjectID:   "11111111-1111-1111-1111-111111111111",
		Kind:        "worker",
		Harness:     "claude-code",
		DisplayName: "ok",
		Prompt:      "do the work",
		Mode:        "trusted",
	}
	if !validSessionInput(base) {
		t.Fatal("baseline request should be valid")
	}

	cases := []struct {
		name        string
		displayName string
		want        bool
	}{
		{"empty rejected", "", false},
		{"single char ok", "a", true},
		{"80 chars ok", strings.Repeat("a", 80), true},
		{"100 chars ok (renderer slice max)", strings.Repeat("a", 100), true},
		{"101 chars rejected", strings.Repeat("a", 101), false},
		// Rune count, not byte length: 100 two-byte runes are 200 bytes but only
		// 100 characters, matching the renderer's char-based slice.
		{"100 multibyte runes ok", strings.Repeat("é", 100), true},
		{"101 multibyte runes rejected", strings.Repeat("é", 101), false},
	}
	for _, tc := range cases {
		req := base
		req.DisplayName = tc.displayName
		if got := validSessionInput(req); got != tc.want {
			t.Errorf("%s: validSessionInput(displayName runes=%d) = %v, want %v",
				tc.name, len([]rune(tc.displayName)), got, tc.want)
		}
	}
}

func TestCreateSessionInitialInterface(t *testing.T) {
	tests := []struct {
		name          string
		harness       string
		interfaceMode string
		wantStatus    int
		wantInterface domain.SessionInterface
		wantCode      string
	}{
		{name: "default terminal", harness: "claude-code", wantStatus: http.StatusCreated, wantInterface: domain.SessionInterfaceTUI},
		{name: "explicit terminal", harness: "claude-code", interfaceMode: "tui", wantStatus: http.StatusCreated, wantInterface: domain.SessionInterfaceTUI},
		{name: "supported chat", harness: "codex", interfaceMode: "chat", wantStatus: http.StatusCreated, wantInterface: domain.SessionInterfaceChat},
		{name: "unsupported chat", harness: "opencode", interfaceMode: "chat", wantStatus: http.StatusUnprocessableEntity, wantCode: "INTERFACE_MODE_UNSUPPORTED"},
		{name: "invalid interface", harness: "claude-code", interfaceMode: "both", wantStatus: http.StatusUnprocessableEntity, wantCode: "validation_error"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			store := &stubAutolinkStore{}
			srv := newChildServer(store, bothProviderProvisioning("nodeops"), "nodeops")
			req := createSessionRequestHTTP(t, "worker", "")
			body, err := io.ReadAll(req.Body)
			if err != nil {
				t.Fatal(err)
			}
			var input map[string]any
			if err := json.Unmarshal(body, &input); err != nil {
				t.Fatal(err)
			}
			input["harness"] = tc.harness
			if tc.interfaceMode != "" {
				input["interfaceMode"] = tc.interfaceMode
			}
			body, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			req.Body = io.NopCloser(strings.NewReader(string(body)))
			recorder := httptest.NewRecorder()
			srv.createSession(recorder, req)
			if recorder.Code != tc.wantStatus {
				t.Fatalf("status = %d, want %d; body = %s", recorder.Code, tc.wantStatus, recorder.Body.String())
			}
			if tc.wantStatus == http.StatusCreated {
				if !store.created || store.captured.Interface != tc.wantInterface {
					t.Fatalf("created = %v, interface = %q, want %q", store.created, store.captured.Interface, tc.wantInterface)
				}
			} else {
				if store.created {
					t.Fatal("invalid request created a session")
				}
				if !strings.Contains(recorder.Body.String(), tc.wantCode) {
					t.Fatalf("body = %s, want error code %q", recorder.Body.String(), tc.wantCode)
				}
			}
		})
	}
}
