package agentaccount

import (
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// TestAntigravityPrepareSharesHistoryNotLogin pins what lets a conversation
// resume under another agy account without one account's sign-in reaching
// another: conversations, settings and MCP config are links, while the sign-in,
// the Google account list, caches and SQLite indexes stay in the account.
func TestAntigravityPrepareSharesHistoryNotLogin(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	cli := filepath.Join(device, "antigravity-cli")
	write(t, filepath.Join(cli, "antigravity-oauth-token"), `{"token":{"refresh_token":"device"}}`)
	write(t, filepath.Join(cli, "conversations", "c1.db"), "db")
	write(t, filepath.Join(cli, "settings.json"), `{"theme":"dark"}`)
	write(t, filepath.Join(cli, "conversation_summaries.db"), "")
	write(t, filepath.Join(cli, "jetski_state.pbtxt"), "")
	write(t, filepath.Join(cli, "log", "cli.log"), "")
	write(t, filepath.Join(cli, "cache", "onboarding.json"), `{"onboardingComplete":true}`)
	write(t, filepath.Join(device, "config", "mcp.json"), "{}")
	write(t, filepath.Join(device, "projects.json"), "{}")
	write(t, filepath.Join(device, "projects.json.1234.tmp"), "{}")
	write(t, filepath.Join(device, "google_accounts.json"), `{"active":"me@example.com"}`)
	write(t, filepath.Join(device, "mcp-oauth-tokens.json"), "{}")

	a := &Antigravity{DeviceHome: device}
	for range 2 {
		if err := a.Prepare(home); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"conversations", "settings.json", "brain", "annotations", "knowledge", "skills"} {
		linksTo(t, filepath.Join(home, "antigravity-cli", name), filepath.Join(cli, name))
		absentLink(t, filepath.Join(home, "antigravity-acp", name), filepath.Join(device, "antigravity-acp", name), name != "settings.json")
	}
	for _, name := range []string{"config", "projects.json"} {
		linksTo(t, filepath.Join(home, name), filepath.Join(device, name))
	}
	for _, name := range []string{"antigravity-oauth-token", "conversation_summaries.db", "jetski_state.pbtxt", "log"} {
		absent(t, filepath.Join(home, "antigravity-cli", name))
	}
	for _, name := range []string{"google_accounts.json", "mcp-oauth-tokens.json", "projects.json.1234.tmp"} {
		absent(t, filepath.Join(home, name))
	}
	for _, dir := range []string{"antigravity-cli", "antigravity-acp"} {
		if info, err := os.Lstat(filepath.Join(home, dir)); err != nil || !info.IsDir() {
			t.Fatalf("%s is not a real folder of the account: %v", dir, err)
		}
	}
	// The first-run screens done on the computer are done on the account.
	if data, err := os.ReadFile(filepath.Join(home, "antigravity-cli", "cache", "onboarding.json")); err != nil || !strings.Contains(string(data), "true") {
		t.Fatalf("onboarding not carried: %q %v", data, err)
	}
}

// absentLink checks that a known shared name links into device, and that an
// entry the computer does not have is not shared.
func absentLink(t *testing.T, link, target string, known bool) {
	t.Helper()
	if known {
		linksTo(t, link, target)
		return
	}
	absent(t, link)
}

// TestAntigravityPrepareLetsACPFindTheSignIn: a sign-in agy made in its
// terminal is found by the account's ACP server too.
func TestAntigravityPrepareLetsACPFindTheSignIn(t *testing.T) {
	device, home := t.TempDir(), filepath.Join(t.TempDir(), "acct")
	token := filepath.Join(home, "antigravity-cli", "antigravity-oauth-token")
	write(t, token, `{"token":{"refresh_token":"r"}}`)
	if err := (&Antigravity{DeviceHome: device}).Prepare(home); err != nil {
		t.Fatal(err)
	}
	linksTo(t, filepath.Join(home, "antigravity-acp", "antigravity-oauth-token"), token)
	absent(t, filepath.Join(device, "antigravity-cli", "antigravity-oauth-token"))
}

func TestAntigravityPrepareNeedsTheComputersFolder(t *testing.T) {
	err := (&Antigravity{}).Prepare(filepath.Join(t.TempDir(), "acct"))
	if !errors.Is(err, ports.ErrAgentAccountUnshared) {
		t.Fatalf("Prepare without a device folder = %v, want ErrAgentAccountUnshared", err)
	}
}

func jwt(t *testing.T, claims string) string {
	t.Helper()
	return "h." + base64.RawURLEncoding.EncodeToString([]byte(claims)) + ".s"
}

// TestAntigravityIdentityReadsTheFolderSignIn: a saved account is who its own
// sign-in file names, by the email agy records, or by an older agy's ID token.
func TestAntigravityIdentityReadsTheFolderSignIn(t *testing.T) {
	a := &Antigravity{DeviceHome: t.TempDir(), Binary: func(context.Context) (string, error) {
		t.Fatal("a saved account's check ran agy")
		return "", nil
	}}
	for _, tc := range []struct {
		name, file, email string
		signedIn          bool
	}{
		{"email", `{"token":{"access_token":"a","refresh_token":"r"},"id_token_email":"me@example.com"}`, "me@example.com", true},
		{"id token", `{"token":{"refresh_token":"r"},"id_token":"` + jwt(t, `{"email":"old@example.com","sub":"1"}`) + `"}`, "old@example.com", true},
		{"no token", `{"token":{},"id_token_email":"me@example.com"}`, "me@example.com", false},
	} {
		home := t.TempDir()
		write(t, filepath.Join(home, "antigravity-cli", "antigravity-oauth-token"), tc.file)
		got, err := a.Identity(context.Background(), home)
		if err != nil || got.SignedIn != tc.signedIn || got.LoggedIn != tc.signedIn || got.Email != tc.email {
			t.Fatalf("%s: identity = %+v, %v", tc.name, got, err)
		}
	}

	got, err := a.Identity(context.Background(), t.TempDir())
	if err != nil || got.SignedIn || got.LoggedIn {
		t.Fatalf("a folder with no sign-in = %+v, %v, want signed out", got, err)
	}

	home := t.TempDir()
	write(t, filepath.Join(home, "antigravity-cli", "antigravity-oauth-token"), `not json SECRET`)
	if _, err := a.Identity(context.Background(), home); err == nil || strings.Contains(err.Error(), "SECRET") {
		t.Fatalf("an unreadable sign-in = %v, want an error that does not quote the file", err)
	}
}

// TestAntigravityDeviceIdentityAsksAgy: the computer's own login lives in the
// keyring, so its check is whether `agy models` works.
func TestAntigravityDeviceIdentityAsksAgy(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script as the CLI")
	}
	bin := filepath.Join(t.TempDir(), "agy")
	write(t, bin, "#!/bin/sh\n[ \"$1\" = models ] || exit 9\nprintf '%s' \"$FAKE_AGY_OUT\"\nexit \"$FAKE_AGY_EXIT\"\n")
	if err := os.Chmod(bin, 0o700); err != nil {
		t.Fatal(err)
	}
	a := &Antigravity{DeviceHome: t.TempDir(), Binary: func(context.Context) (string, error) { return bin, nil }}

	t.Setenv("FAKE_AGY_OUT", "gemini-3-pro")
	t.Setenv("FAKE_AGY_EXIT", "0")
	if got, err := a.Identity(context.Background(), ""); err != nil || !got.SignedIn || !got.LoggedIn {
		t.Fatalf("working agy models = %+v, %v, want signed in", got, err)
	}
	t.Setenv("FAKE_AGY_OUT", "Authentication required. Please sign in.")
	t.Setenv("FAKE_AGY_EXIT", "1")
	if got, err := a.Identity(context.Background(), ""); err != nil || got.SignedIn || got.LoggedIn {
		t.Fatalf("agy asking for a sign-in = %+v, %v, want signed out", got, err)
	}
	t.Setenv("FAKE_AGY_OUT", "network unreachable")
	if _, err := a.Identity(context.Background(), ""); err == nil {
		t.Fatal("another agy failure read as an answer")
	}
}

// TestAntigravityDeviceIdentityReadsAFileSignIn: where agy keeps the computer's
// own login in a file (Linux without a keyring), that file answers.
func TestAntigravityDeviceIdentityReadsAFileSignIn(t *testing.T) {
	device := t.TempDir()
	write(t, filepath.Join(device, "antigravity-cli", "antigravity-oauth-token"), `{"token":{"refresh_token":"r"},"id_token_email":"box@example.com"}`)
	a := &Antigravity{DeviceHome: device, Binary: func(context.Context) (string, error) { return "", errors.New("not run") }}
	if got, err := a.Identity(context.Background(), ""); err != nil || !got.SignedIn || got.Email != "box@example.com" {
		t.Fatalf("device identity = %+v, %v", got, err)
	}
}

func TestAntigravityLogoutRemovesOnlyTheFolderSignIn(t *testing.T) {
	home := t.TempDir()
	for _, dir := range []string{"antigravity-cli", "antigravity-acp"} {
		write(t, filepath.Join(home, dir, "antigravity-oauth-token"), "{}")
	}
	write(t, filepath.Join(home, "antigravity-cli", "settings.json"), "{}")
	a := &Antigravity{}
	if err := a.Logout(context.Background(), home); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"antigravity-cli", "antigravity-acp"} {
		absent(t, filepath.Join(home, dir, "antigravity-oauth-token"))
	}
	if _, err := os.Stat(filepath.Join(home, "antigravity-cli", "settings.json")); err != nil {
		t.Fatalf("logout removed more than the sign-in: %v", err)
	}
	if err := a.Logout(context.Background(), ""); err == nil {
		t.Fatal("signing out the computer's own login succeeded")
	}
}

func TestAntigravityLaunchAndSignInNameTheFolder(t *testing.T) {
	a := &Antigravity{Binary: func(context.Context) (string, error) { return "/bin/agy", nil }}
	if env := a.LaunchEnv(""); env != nil {
		t.Fatalf("device launch env = %v, want none", env)
	}
	want := map[string]string{"GEMINI_HOME": "/acct", "AGY_CLI_FORCE_FILE_TOKEN_STORAGE": "true"}
	if env := a.LaunchEnv("/acct"); !reflect.DeepEqual(env, want) {
		t.Fatalf("launch env = %v, want %v", env, want)
	}
	argv, err := a.HomeLoginArgv(context.Background(), "/acct")
	if err != nil || !reflect.DeepEqual(argv, []string{"/bin/agy", "--gemini-dir", "/acct"}) {
		t.Fatalf("sign-in argv = %v, %v", argv, err)
	}
	if _, err := a.Limits(context.Background(), "/acct"); !errors.Is(err, ports.ErrAgentAccountNoLimits) {
		t.Fatalf("limits = %v, want ErrAgentAccountNoLimits", err)
	}
	if a.HasLimits() {
		t.Fatal("Antigravity reports limits")
	}
}
