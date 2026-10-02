//go:build !windows

package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestAgentProcessDottedSessionStartsChild(t *testing.T) {
	cfg := setConfigEnv(t)
	srv, _ := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	out, _, err := executeCLI(t, Deps{In: strings.NewReader(""), ProcessAlive: func(int) bool { return true }},
		"agent-process", "supervise", "--session", "fixture.project-1", "--launch", "launch-1", "--", "printf", "child-started")
	if err != nil || out != "child-started" {
		t.Fatalf("dotted session did not start its child: err=%v output=%q", err, out)
	}
}

func TestDottedSessionHookPreservesLaunchFence(t *testing.T) {
	for _, launch := range []string{"launch-1", "launch.1"} {
		t.Run(launch, func(t *testing.T) {
			t.Setenv("AO_SESSION_ID", "fixture.project-1")
			t.Setenv("AO_RUNTIME_LAUNCH_ID", launch)
			cfg := setConfigEnv(t)
			srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
			writeRunFileFor(t, cfg, srv)
			_, _, err := executeCLI(t, Deps{In: strings.NewReader(`{"session_id":"fixture-native"}`), ProcessAlive: func(int) bool { return true }}, "hooks", "codex", "session-start")
			if err != nil || capture.hits != 1 || !strings.HasSuffix(capture.path, "/sessions/fixture.project-1/activity") {
				t.Fatalf("dotted session hook was not delivered: err=%v hits=%d", err, capture.hits)
			}
			var request setActivityAPIRequest
			if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
				t.Fatal(err)
			}
			want := launch
			if launch == "launch.1" {
				want = ""
			}
			if request.LaunchID != want {
				t.Fatal("hook launch validation changed")
			}
		})
	}
}

func TestAgentProcessDottedValidationPreservesUnsafeRejection(t *testing.T) {
	for _, tc := range []struct{ session, launch string }{
		{"../fixture", "launch-1"}, {"fixture/1", "launch-1"}, {".hidden", "launch-1"},
		{"fixture project", "launch-1"}, {"fixture?query", "launch-1"}, {"fixture#fragment", "launch-1"},
		{"fixture-1", "launch.1"}, {"fixture-1", "../launch"},
	} {
		_, _, err := executeCLI(t, Deps{}, "agent-process", "supervise", "--session", tc.session,
			"--launch", tc.launch, "--", "printf", "must-not-start")
		if err == nil {
			t.Fatal("unsafe session or launch identifier accepted")
		}
	}
}
