package proxyhost

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"testing"
)

// onlyThisComputer hides the real machine's agents from a test and returns the
// directories Codex and Claude Code keep their files in.
func onlyThisComputer(t *testing.T) (codexHome, claudeHome string) {
	t.Helper()
	codexHome, claudeHome = t.TempDir(), t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("CLAUDE_CONFIG_DIR", claudeHome)
	return codexHome, claudeHome
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func jwtForTest(payload string) string {
	return "eyJhbGciOiJub25lIn0." + base64.RawURLEncoding.EncodeToString([]byte(payload)) + ".sig"
}

// The computer's own login is only named, never copied: the helper would renew
// a copy and sign the computer's agent out.
func TestDeviceEmailNamesTheComputersLoginAndCopiesNothing(t *testing.T) {
	codexAuth := `{"auth_mode":"chatgpt","tokens":{"access_token":"access","refresh_token":"refresh","id_token":"` + jwtForTest(`{"email":"Native@Example.test"}`) + `"}}`
	for name, tc := range map[string]struct {
		provider, codexFile, claudeFile, want string
	}{
		"Codex":                  {"codex", codexAuth, "", "native@example.test"},
		"Codex signed out":       {"codex", "", "", ""},
		"Codex with an API key":  {"codex", `{"auth_mode":"apikey","OPENAI_API_KEY":"sk-test"}`, "", ""},
		"Codex file unreadable":  {"codex", "{", "", ""},
		"Claude":                 {"claude", "", `{"oauthAccount":{"emailAddress":" Me@Example.test "}}`, "me@example.test"},
		"Claude never logged in": {"claude", "", `{"numStartups":3}`, ""},
		"Claude with no file":    {"claude", "", "", ""},
	} {
		t.Run(name, func(t *testing.T) {
			codexHome, claudeHome := onlyThisComputer(t)
			if tc.codexFile != "" {
				writeFile(t, codexHome, "auth.json", tc.codexFile)
			}
			if tc.claudeFile != "" {
				writeFile(t, claudeHome, ".claude.json", tc.claudeFile)
			}
			c, helper := helperClient(t, func(received call) (int, string) {
				t.Errorf("reading the computer's login called the helper: %s", received.line())
				return 500, ""
			})
			if got := c.DeviceEmail(ctx, tc.provider); got != tc.want {
				t.Fatalf("email=%q want %q", got, tc.want)
			}
			if lines := helper.lines(); len(lines) != 0 {
				t.Fatalf("calls=%v", lines)
			}
		})
	}
}
