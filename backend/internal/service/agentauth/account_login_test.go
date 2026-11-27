package agentauth

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// TestAgyGuidedMethodsAddAccountsWhereTheyRun: agy's guided sign-in adds an
// Antigravity account where it runs; elsewhere accounts keep the terminal.
func TestAgyGuidedMethodsAddAccountsWhereTheyRun(t *testing.T) {
	for goos, want := range map[string]bool{"darwin": true, "windows": true, "linux": false} {
		t.Run(goos, func(t *testing.T) {
			onPlatform(t, goos)
			for _, method := range []string{"browser", "cloud_project"} {
				if got := AccountLoginMethod("agy", method); got != want {
					t.Fatalf("AccountLoginMethod(agy, %s) = %v, want %v", method, got, want)
				}
			}
			if AccountLoginMethod("agy", "native") {
				t.Fatal("agy's native sign-in adds an account")
			}
		})
	}
}

// TestAgyAccountLoginRunsInTheAccountFolderAndFinishesOnItsSignIn: an
// account's guided login runs agy in the account's folder, and finishes only
// once that folder holds a sign-in, whatever the computer's own login says.
func TestAgyAccountLoginRunsInTheAccountFolderAndFinishesOnItsSignIn(t *testing.T) {
	onPlatform(t, "darwin")

	home := t.TempDir()
	pty := &guidedTerminalFake{output: agyMenuScreen}
	// The computer's own login is signed in; it must not finish the account's.
	prober := &switchableProberFake{state: domain.AgentAuthenticationAuthorized}
	svc := NewWithAgentResolver(foundExecutable("agy"), prober, pty, "")
	t.Cleanup(svc.Close)

	env := map[string]string{"GEMINI_HOME": home, "AGY_CLI_FORCE_FILE_TOKEN_STORAGE": "true"}
	started, err := svc.StartAccountLogin(t.Context(), "agy", "browser", "", env)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"/test/bin/agy", "--gemini_dir=" + home}; !pty.input.HideFromList || !reflect.DeepEqual(pty.input.Argv, want) {
		t.Fatalf("terminal input = %+v, want hidden %v", pty.input, want)
	}
	if pty.input.Env["GEMINI_HOME"] != home || pty.input.Env["AGY_CLI_FORCE_FILE_TOKEN_STORAGE"] != "true" {
		t.Fatalf("terminal env = %v, want the account's folder and file sign-in", pty.input.Env)
	}

	waitFor(t, "Enter on Agy's login menu", 5*time.Second, func() bool { return len(pty.pressed()) > 0 })
	pty.setOutput(agySignedInChat)
	// Several probe rounds pass with only the computer's login signed in.
	time.Sleep(3 * time.Second)
	if current, err := svc.Login(t.Context(), "agy", started.ID); err != nil || current.Status != "running" {
		t.Fatalf("snapshot = %+v, %v; want running until the account's folder is signed in", current, err)
	}

	token := filepath.Join(home, "antigravity-cli", "antigravity-oauth-token")
	if err := os.MkdirAll(filepath.Dir(token), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(token, []byte(`{"token":{"refresh_token":"r"},"id_token_email":"new@example.com"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var current LoginSnapshot
	waitFor(t, "the account's sign-in to finish", 10*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.Status != "running"
	})
	if current.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", current.Status)
	}
}
