package agentauth

import (
	"context"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// switchableProberFake reports a sign-in state the test can change while a
// login is running.
type switchableProberFake struct {
	mu    sync.Mutex
	state domain.AgentAuthenticationState
}

func (f *switchableProberFake) set(state domain.AgentAuthenticationState) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.state = state
}

func (f *switchableProberFake) ResolveAgentBinary(_ context.Context, agentID string) (string, error) {
	return "/test/bin/" + agentID, nil
}
func (f *switchableProberFake) InvalidateAgentAuthentication(string) {}
func (f *switchableProberFake) EnsureAgentReadiness(context.Context, string, domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return domain.AgentReadinessSnapshot{Authentication: domain.AgentAuthenticationObservation{State: f.state}}, nil
}

const agyMenuScreen = "Welcome to the Antigravity CLI. You are currently not signed in.\n" +
	"Select login method:\n> 1. Google OAuth\n  2. Use a Google Cloud project\n↑/↓ Navigate · enter Select\n"

// agyWrappedLink is how Agy prints its sign-in link: wrapped across
// full-width lines, followed by its viewport footer.
var agyWrappedLink = []string{
	"https://accounts.google.com/o/oauth2/auth?access_type=offline&client_id=107100",
	"6060591-example.apps.googleusercontent.com&code_challenge=abc&code_challenge_m",
	"ethod=S256&redirect_uri=https%3A%2F%2Fantigravity.google%2Foauth-callback&stat",
	"e=xyz",
}

const agyLink = "https://accounts.google.com/o/oauth2/auth?access_type=offline&client_id=1071006060591-example.apps.googleusercontent.com&code_challenge=abc&code_challenge_method=S256&redirect_uri=https%3A%2F%2Fantigravity.google%2Foauth-callback&state=xyz"

func waitFor(t *testing.T, what string, timeout time.Duration, done func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if done() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func (f *guidedTerminalFake) setOutput(output string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.output = output
}

func (f *guidedTerminalFake) pressed() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.keys...)
}

func (f *guidedTerminalFake) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func TestAgyLoginMethodsListGoogleAccountAndCloudProject(t *testing.T) {
	t.Parallel()

	var ids []string
	for _, method := range loginMethods("agy") {
		ids = append(ids, method.ID)
		if method.Secret {
			t.Fatalf("method %q asks for a secret", method.ID)
		}
	}
	if want := []string{"browser", "cloud_project", "native"}; !reflect.DeepEqual(ids, want) {
		t.Fatalf("agy methods = %v, want %v", ids, want)
	}
}

func TestAgyGoogleLoginDrivesMenuShowsLinkAndFinishesWhileAgyRuns(t *testing.T) {
	t.Parallel()

	pty := &guidedTerminalFake{output: agyMenuScreen}
	prober := &switchableProberFake{state: domain.AgentAuthenticationUnknown}
	svc := NewWithAgentResolver(foundExecutable("agy"), prober, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	if started.Method != "browser" || started.NativePrompt != "" {
		t.Fatalf("started = %+v, want a guided browser login without native prompts", started)
	}
	if !pty.input.HideFromList || !reflect.DeepEqual(pty.input.Argv, []string{"/test/bin/agy"}) {
		t.Fatalf("terminal input = %+v, want hidden bare agy", pty.input)
	}

	waitFor(t, "Enter on Agy's login menu", 5*time.Second, func() bool { return len(pty.pressed()) > 0 })
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"select"}) {
		t.Fatalf("keys = %v, want [select] for the highlighted Google OAuth entry", got)
	}

	pty.setOutput(agyMenuScreen + "Your browser should open automatically. If not:\n" + strings.Join(agyWrappedLink, "\n") + "\n(1–20 of 29 lines)\n")
	var current LoginSnapshot
	waitFor(t, "the printed sign-in link", 5*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.LoginURL != ""
	})
	if current.LoginURL != agyLink || current.Status != "running" {
		t.Fatalf("snapshot = %+v, want running with the stitched link", current)
	}

	// Agy stays open in its chat view after signing in: the probe finishes it.
	prober.set(domain.AgentAuthenticationAuthorized)
	waitFor(t, "the sign-in to finish", 10*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.Status != "running"
	})
	if current.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", current.Status)
	}
	waitFor(t, "the hidden terminal to close", 5*time.Second, pty.isClosed)
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"select"}) {
		t.Fatalf("keys after sign-in = %v, want no further input", got)
	}
}

func TestAgyCloudProjectLoginWalksBothMenus(t *testing.T) {
	t.Parallel()

	pty := &guidedTerminalFake{output: agyMenuScreen}
	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{state: domain.AgentAuthenticationUnknown}, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "cloud_project", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the project entry to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 2 })
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"next", "select"}) {
		t.Fatalf("keys = %v, want [next select]", got)
	}

	pty.setOutput(agyMenuScreen + "Google Cloud sign-in method:\n> 1. Continue with Google Cloud\n  2. Use advanced SSO config\n· esc Back\n")
	waitFor(t, "the Google Cloud entry to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 3 })
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"next", "select", "select"}) {
		t.Fatalf("keys = %v, want [next select select]", got)
	}
	if _, err := svc.CancelLogin(t.Context(), "agy", started.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedLoginWaitsForItsMenu(t *testing.T) {
	t.Parallel()

	pty := &guidedTerminalFake{output: "⣷  Signing in...\n"}
	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{state: domain.AgentAuthenticationUnknown}, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(1500 * time.Millisecond)
	if got := pty.pressed(); len(got) != 0 {
		t.Fatalf("keys = %v before the menu appeared", got)
	}
	if _, err := svc.CancelLogin(t.Context(), "agy", started.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedLoginRefusesCredentials(t *testing.T) {
	t.Parallel()

	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{}, &guidedTerminalFake{}, "")
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "agy", "browser", "secret"); err == nil {
		t.Fatal("guided login accepted a credential")
	}
}

func TestPrintedLoginURL(t *testing.T) {
	t.Parallel()

	wrapped := strings.Join(agyWrappedLink, "\n")
	tests := []struct {
		name, output, want string
	}{
		{"stitches a wrapped link", "If not:\n" + wrapped + "\n(1–20 of 29 lines)\n", agyLink},
		{"stops at a short last line", wrapped + "\nCopy\n", agyLink},
		{"single line", "Open https://accounts.google.com/o/oauth2/auth?client_id=x in your browser\n", "https://accounts.google.com/o/oauth2/auth?client_id=x"},
		{"rejects other hosts", "https://evil.example.com/o/oauth2/auth?x=1\n", ""},
		{"rejects other paths", "https://accounts.google.com/signin/v2?x=1\n", ""},
		{"no link", "Signing in...\n", ""},
	}
	for _, tt := range tests {
		if got := printedLoginURL("agy", tt.output); got != tt.want {
			t.Errorf("%s: printedLoginURL = %q, want %q", tt.name, got, tt.want)
		}
	}
	if got := printedLoginURL("goose", "https://accounts.google.com/o/oauth2/auth?x=1\n"); got != "" {
		t.Errorf("a harness without link rules got %q", got)
	}
}
