package cli

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestManagedAccountsCredentialMethodError(t *testing.T) {
	for _, asJSON := range []bool{false, true} {
		t.Run(map[bool]string{false: "human", true: "json"}[asJSON], func(t *testing.T) {
			cfg := setConfigEnv(t)
			capture := &agentSwitchRequestCapture{}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capture.record(r)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusBadRequest)
				_, _ = io.WriteString(w, `{"code":"ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED","message":"synthetic-only at http://127.0.0.1:9999/private","requestId":"credential-format-79"}`)
			}))
			t.Cleanup(server.Close)
			writeRunFileFor(t, cfg, server)
			args := []string{"accounts", "add-key", "--provider", "codex", "--operation-id", "wrong-method", "--stdin"}
			if asJSON {
				args = append(args, "--json")
			}
			out, errOut, err := executeCLI(t, Deps{In: strings.NewReader("sk-ant-oat01-synthetic-only"), ProcessAlive: func(int) bool { return true }}, args...)
			_, _, _, calls := capture.snapshot()
			if ExitCode(err) != 1 || calls != 1 || out != "" || !strings.Contains(err.Error(), "ACCOUNTS_MANAGER_CREDENTIAL_METHOD_UNSUPPORTED") || !strings.Contains(err.Error(), "credential-format-79") {
				t.Fatalf("method error lost daemon correlation or changed exit semantics: %v calls=%d", err, calls)
			}
			for _, privateValue := range []string{"synthetic-only", "127.0.0.1", "/private"} {
				if strings.Contains(out+errOut+err.Error(), privateValue) {
					t.Error("credential or private diagnostics exposed")
				}
			}
		})
	}
}
