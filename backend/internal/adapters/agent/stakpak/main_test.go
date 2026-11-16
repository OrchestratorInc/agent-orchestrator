package stakpak

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeStakpakDirEnv points a re-executed test binary at the fake CLI's state.
const fakeStakpakDirEnv = "AO_FAKE_STAKPAK_DIR"

// TestMain lets the test binary stand in for the stakpak CLI. fakeStakpak puts
// a copy on PATH under Stakpak's own executable name, so binary discovery, the
// identity check and the session commands run as they do in production, on
// every platform.
func TestMain(m *testing.M) {
	if dir := os.Getenv(fakeStakpakDirEnv); dir != "" {
		os.Exit(runFakeStakpak(dir, os.Args[1:]))
	}
	os.Exit(m.Run())
}

// runFakeStakpak answers `--version`, `sessions list`, `sessions show <id>`
// and `auth status` from the files fakeStakpak wrote, logging every call to calls.log. A
// `broken` file makes every call fail.
func runFakeStakpak(dir string, args []string) int {
	if f, err := os.OpenFile(filepath.Join(dir, "calls.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600); err == nil { //nolint:gosec // test-only path
		_, _ = fmt.Fprintln(f, strings.Join(args, " "))
		_ = f.Close()
	}
	if _, err := os.Stat(filepath.Join(dir, "broken")); err == nil {
		return 1
	}
	// The "remote" profile's store is the Stakpak API, which refuses a
	// caller without STAKPAK_API_KEY, or with the key named "revoked".
	if key := os.Getenv("STAKPAK_API_KEY"); len(args) > 0 && args[0] == "sessions" && os.Getenv("STAKPAK_PROFILE") == "remote" && (key == "" || key == "revoked") {
		_, _ = fmt.Fprintln(os.Stderr, "Error: API error 401 Unauthorized")
		return 1
	}
	switch {
	case len(args) > 0 && args[0] == "--version":
		fmt.Println("stakpak 0.3.88")
	case len(args) > 1 && args[0] == "sessions" && args[1] == "list":
		// sessions.json, or sessions-<profile>.json under STAKPAK_PROFILE.
		name := "sessions.json"
		if profile := os.Getenv("STAKPAK_PROFILE"); profile != "" {
			name = "sessions-" + profile + ".json"
		}
		list, err := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // test-only path
		if err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			return 1
		}
		_, _ = os.Stdout.Write(list)
	case len(args) > 2 && args[0] == "sessions" && args[1] == "show":
		// Each profile has its own store: known-session, or
		// known-session-<profile> under STAKPAK_PROFILE.
		name := "known-session"
		if profile := os.Getenv("STAKPAK_PROFILE"); profile != "" {
			name += "-" + profile
		}
		known, _ := os.ReadFile(filepath.Join(dir, name)) //nolint:gosec // test-only path
		if len(known) == 0 || args[2] != string(known) {
			_, _ = fmt.Fprintf(os.Stderr, "Error: Not found: Session %s not found\n", args[2])
			return 1
		}
		fmt.Printf("ID:          %s\n", args[2])
	case len(args) > 1 && args[0] == "auth" && args[1] == "status":
		// What Stakpak prints for a profile with no stored credential.
		fmt.Println("No credentials configured.")
	}
	return 0
}
