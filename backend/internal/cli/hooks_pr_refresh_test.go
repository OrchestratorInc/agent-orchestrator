package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestPostToolUseHookFlagsPRChangingCommands(t *testing.T) {
	t.Setenv("AO_SESSION_ID", "qa-1")
	t.Setenv("AO_RUNTIME_LAUNCH_ID", "launch-1")
	cfg := setConfigEnv(t)
	srv, capture := activityServer(t, http.StatusOK, `{"ok":true}`)
	writeRunFileFor(t, cfg, srv)
	for _, tc := range []struct {
		event, command string
		want           bool
	}{
		{"post-tool-use", `"gh pr merge --squash"`, true},
		{"post-tool-use", `["bash","-lc","git push"]`, true},
		{"post-tool-use", `"gh pr view 3"`, false},
		{"pre-tool-use", `"gh pr merge --squash"`, false},
	} {
		payload := `{"tool_name":"Bash","tool_use_id":"t1","tool_input":{"command":` + tc.command + `}}`
		if _, _, err := executeCLI(t, Deps{In: strings.NewReader(payload), ProcessAlive: func(int) bool { return true }}, "hooks", "claude-code", tc.event); err != nil {
			t.Fatal(err)
		}
		var request setActivityAPIRequest
		if err := json.Unmarshal([]byte(capture.body), &request); err != nil {
			t.Fatal(err)
		}
		if request.PRMayHaveChanged != tc.want {
			t.Fatalf("%s %s: prMayHaveChanged = %v, want %v", tc.event, tc.command, request.PRMayHaveChanged, tc.want)
		}
	}
}
