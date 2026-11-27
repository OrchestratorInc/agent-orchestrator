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
	return domain.AgentReadinessSnapshot{Authentication: domain.AgentAuthenticationObservation{State: f.state, Freshness: domain.AgentReadinessFresh}}, nil
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

// onPlatform pins the platform guided sign-in is checked against. Tests using
// it must not run in parallel.
func onPlatform(t *testing.T, goos string) {
	t.Helper()
	previous := currentGOOS
	currentGOOS = goos
	t.Cleanup(func() { currentGOOS = previous })
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

func (f *guidedTerminalFake) openCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.opens
}

func (f *guidedTerminalFake) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func TestAgyLoginMethodsListGoogleAccountAndCloudProject(t *testing.T) {
	for _, goos := range []string{"darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			onPlatform(t, goos)

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
		})
	}
}

func TestAgyGoogleLoginDrivesMenuShowsLinkAndFinishesWhileAgyRuns(t *testing.T) {
	onPlatform(t, "darwin")

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
	onPlatform(t, "darwin")

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

func TestGuidedLoginFinishesOnlyAfterItsStepsAreSent(t *testing.T) {
	onPlatform(t, "darwin")

	pty := &guidedTerminalFake{output: agyMenuScreen}
	prober := &switchableProberFake{state: domain.AgentAuthenticationUnknown}
	svc := NewWithAgentResolver(foundExecutable("agy"), prober, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "cloud_project", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the project entry to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 2 })
	// A Google sign-in now passes the probe while the Cloud menu is pending.
	prober.set(domain.AgentAuthenticationAuthorized)
	// The Cloud menu never appears: past several probe rounds the login must
	// still be waiting on its last step instead of reporting success.
	time.Sleep(3 * time.Second)
	current, err := svc.Login(t.Context(), "agy", started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "running" || pty.isClosed() {
		t.Fatalf("status = %q closed = %v with a menu step unsent, want still running", current.Status, pty.isClosed())
	}

	pty.setOutput(agyMenuScreen + "Google Cloud sign-in method:\n> 1. Continue with Google Cloud\n  2. Use advanced SSO config\n")
	waitFor(t, "the sign-in to finish", 10*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.Status != "running"
	})
	if current.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", current.Status)
	}
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"next", "select", "select"}) {
		t.Fatalf("keys = %v, want every reviewed step sent before finishing", got)
	}
}

func TestGuidedLoginThatExitsWithStepsUnsentFails(t *testing.T) {
	onPlatform(t, "darwin")

	pty := &guidedTerminalFake{output: agyMenuScreen}
	prober := &switchableProberFake{state: domain.AgentAuthenticationUnknown}
	svc := NewWithAgentResolver(foundExecutable("agy"), prober, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "cloud_project", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the project entry to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 2 })
	// A Google sign-in now passes the probe.
	prober.set(domain.AgentAuthenticationAuthorized)
	// Agy exits before its Cloud menu appears.
	pty.mu.Lock()
	pty.alive = false
	pty.mu.Unlock()
	var current LoginSnapshot
	waitFor(t, "the login to end", 5*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.Status != "running"
	})
	if current.Status != "failed" {
		t.Fatalf("status = %q, want failed with a reviewed step unsent", current.Status)
	}
}

const agyTrustScreen = "Accessing workspace:\n/tmp/x\nDo you trust the contents of this project?\n> Yes, I trust this folder\n  No, exit\n"

const agyChatScreen = "Antigravity CLI 1.5.18\n> \n? for shortcuts                auto · Gemini · high\n"

func TestGuidedLoginReplacesAnExistingSignIn(t *testing.T) {
	onPlatform(t, "darwin")
	fastGuidedDelays(t)

	// Signed in, Agy skips its menu and asks about the fresh folder.
	pty := &guidedTerminalFake{output: agyTrustScreen}
	prober := &switchableProberFake{state: domain.AgentAuthenticationAuthorized}
	svc := NewWithAgentResolver(foundExecutable("agy"), prober, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the folder to be trusted", 5*time.Second, func() bool { return len(pty.pressed()) >= 1 })
	pty.setOutput(agyChatScreen)
	waitFor(t, "the sign-out", 5*time.Second, func() bool { return len(pty.pressed()) >= 3 })
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"select", "type:/logout", "select"}) {
		t.Fatalf("keys = %v, want [select type:/logout select]", got)
	}
	// Until the sign-out lands, Agy is not restarted and the old sign-in
	// does not finish the login.
	time.Sleep(2500 * time.Millisecond)
	current, err := svc.Login(t.Context(), "agy", started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "running" || pty.openCount() != 1 {
		t.Fatalf("status = %q opens = %d while signing out, want running in the first agy", current.Status, pty.openCount())
	}

	// Signed out, Agy returns to its chat; a fresh agy shows the menu.
	pty.setOutput(agyChatScreen)
	prober.set(domain.AgentAuthenticationUnknown)
	waitFor(t, "a fresh agy", 5*time.Second, func() bool { return pty.openCount() == 2 })
	pty.setOutput(agyMenuScreen)
	waitFor(t, "Google OAuth to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 4 })
	prober.set(domain.AgentAuthenticationAuthorized)
	waitFor(t, "the sign-in to finish", 10*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.Status != "running"
	})
	if current.Status != "succeeded" {
		t.Fatalf("status = %q, want succeeded", current.Status)
	}
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"select", "type:/logout", "select", "select"}) {
		t.Fatalf("keys = %v, want trust, /logout, then Google OAuth", got)
	}
}

func TestGuidedLoginSkipsTrustWhenAgyOpensItsChat(t *testing.T) {
	onPlatform(t, "darwin")
	fastGuidedDelays(t)

	pty := &guidedTerminalFake{output: agyChatScreen}
	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{state: domain.AgentAuthenticationUnknown}, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "cloud_project", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "the sign-out", 5*time.Second, func() bool { return len(pty.pressed()) >= 2 })
	waitFor(t, "a fresh agy", 5*time.Second, func() bool { return pty.openCount() == 2 })
	pty.setOutput(agyMenuScreen)
	waitFor(t, "the project entry", 5*time.Second, func() bool { return len(pty.pressed()) >= 4 })
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"type:/logout", "select", "next", "select"}) {
		t.Fatalf("keys = %v, want /logout then the project entry", got)
	}
	if _, err := svc.CancelLogin(t.Context(), "agy", started.ID); err != nil {
		t.Fatal(err)
	}
}

func TestGuidedLoginDoesNotSignOutAfterAFreshSignIn(t *testing.T) {
	onPlatform(t, "darwin")

	pty := &guidedTerminalFake{output: agyMenuScreen}
	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{state: domain.AgentAuthenticationUnknown}, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Google OAuth to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 1 })
	// After signing in Agy asks about the folder and opens its chat: that is
	// the new sign-in, not one to replace.
	pty.setOutput(agyTrustScreen)
	time.Sleep(1500 * time.Millisecond)
	pty.setOutput(agyChatScreen)
	time.Sleep(1500 * time.Millisecond)
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"select"}) {
		t.Fatalf("keys = %v, want no sign-out after the new sign-in", got)
	}
	if _, err := svc.CancelLogin(t.Context(), "agy", started.ID); err != nil {
		t.Fatal(err)
	}
}

const agyCodePrompt = "Your browser should open automatically. If not:\nhttps://accounts.google.com/o/oauth2/auth?client_id=x\n" +
	"If you aren't automatically redirected, paste the authorization code below:\nauthorization code...\n"

func TestAgyLoginTakesTheAuthorizationCodeFromThePanel(t *testing.T) {
	onPlatform(t, "darwin")
	previous := commandSettleDelay
	commandSettleDelay = 10 * time.Millisecond
	t.Cleanup(func() { commandSettleDelay = previous })

	pty := &guidedTerminalFake{output: agyMenuScreen}
	prober := &switchableProberFake{state: domain.AgentAuthenticationUnknown}
	svc := NewWithAgentResolver(foundExecutable("agy"), prober, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Google OAuth to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 1 })
	pty.setOutput(agyCodePrompt)
	var current LoginSnapshot
	waitFor(t, "the code box", 5*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.AcceptsCode
	})
	if _, err := svc.SubmitLoginCode(t.Context(), "agy", started.ID, " 4/0AbC_def-123 "); err != nil {
		t.Fatal(err)
	}
	if got := pty.pressed(); !reflect.DeepEqual(got, []string{"select", "code:4/0AbC_def-123", "select"}) {
		t.Fatalf("keys = %v, want the code typed then Enter", got)
	}
	if _, err := svc.SubmitLoginCode(t.Context(), "agy", started.ID, "4/0AbC_def-123"); err == nil {
		t.Fatal("accepted a second code")
	}
	prober.set(domain.AgentAuthenticationAuthorized)
	waitFor(t, "the sign-in to finish", 10*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.Status != "running"
	})
	if current.Status != "succeeded" || current.LoginURL != "" {
		t.Fatalf("snapshot = %+v, want succeeded without the link", current)
	}
}

func TestAgyLoginFailsWhenAgyRejectsTheCode(t *testing.T) {
	onPlatform(t, "darwin")
	previous := commandSettleDelay
	commandSettleDelay = 10 * time.Millisecond
	t.Cleanup(func() { commandSettleDelay = previous })

	pty := &guidedTerminalFake{output: agyMenuScreen}
	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{state: domain.AgentAuthenticationUnknown}, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Google OAuth to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 1 })
	pty.setOutput(agyCodePrompt)
	waitFor(t, "the code box", 5*time.Second, func() bool {
		current, err := svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.AcceptsCode
	})
	if _, err := svc.SubmitLoginCode(t.Context(), "agy", started.ID, "4/0wrongcode"); err != nil {
		t.Fatal(err)
	}
	pty.setOutput("Got an error: token exchange failed: oauth2: \"invalid_grant\" \"Bad Request\"\nPress any key to go back.\n")
	var current LoginSnapshot
	waitFor(t, "the login to end", 5*time.Second, func() bool {
		current, err = svc.Login(t.Context(), "agy", started.ID)
		return err == nil && current.Status != "running"
	})
	if current.Status != "failed" {
		t.Fatalf("status = %q, want failed after a rejected code", current.Status)
	}
}

func (f *guidedTerminalFake) SendAuthTerminalCode(_ context.Context, _ string, code string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, "code:"+code)
	return nil
}

// staleProberFake reports the last sign-in it saw as a stale observation,
// as the readiness coordinator does when a check is inconclusive.
type staleProberFake struct{ switchableProberFake }

func (f *staleProberFake) EnsureAgentReadiness(context.Context, string, domain.AgentReadinessPurpose) (domain.AgentReadinessSnapshot, error) {
	return domain.AgentReadinessSnapshot{Authentication: domain.AgentAuthenticationObservation{State: domain.AgentAuthenticationAuthorized, Freshness: domain.AgentReadinessStale}}, nil
}

func TestGuidedLoginIgnoresAStaleSignIn(t *testing.T) {
	onPlatform(t, "darwin")

	// After /logout Agy's probe is inconclusive, and readiness keeps the old
	// "authorized" state marked stale: that must not finish the login.
	pty := &guidedTerminalFake{output: agyMenuScreen}
	svc := NewWithAgentResolver(foundExecutable("agy"), &staleProberFake{}, pty, "")
	t.Cleanup(svc.Close)

	started, err := svc.StartLogin(t.Context(), "agy", "browser", "")
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, "Google OAuth to be chosen", 5*time.Second, func() bool { return len(pty.pressed()) >= 1 })
	time.Sleep(3 * time.Second)
	current, err := svc.Login(t.Context(), "agy", started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "running" {
		t.Fatalf("status = %q from a stale sign-in, want running", current.Status)
	}
	if _, err := svc.CancelLogin(t.Context(), "agy", started.ID); err != nil {
		t.Fatal(err)
	}
}

// fastGuidedDelays shortens the waits around typed commands and restarts.
// Tests using it must not run in parallel.
func fastGuidedDelays(t *testing.T) {
	t.Helper()
	command, restart := commandSettleDelay, restartCheckDelay
	commandSettleDelay, restartCheckDelay = 10*time.Millisecond, 50*time.Millisecond
	t.Cleanup(func() { commandSettleDelay, restartCheckDelay = command, restart })
}

func (f *guidedTerminalFake) SendAuthTerminalCommand(_ context.Context, _ string, command string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.keys = append(f.keys, "type:"+command)
	return nil
}

func TestGuidedLoginWaitsForItsMenu(t *testing.T) {
	onPlatform(t, "darwin")

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
	onPlatform(t, "darwin")

	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{}, &guidedTerminalFake{}, "")
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "agy", "browser", "secret"); err == nil {
		t.Fatal("guided login accepted a credential")
	}
}

func TestAgyWithoutSignInProbeKeepsPlainNativeSignIn(t *testing.T) {
	onPlatform(t, "linux")

	methods := loginMethods("agy")
	if len(methods) != 1 || methods[0].ID != "native" {
		t.Fatalf("agy methods on linux = %+v, want only native sign-in", methods)
	}
	pty := &guidedTerminalFake{output: agyMenuScreen}
	// Without a sign-in probe Agy reports an installed binary as authorized, so
	// a login that finished while Agy runs would succeed before any sign-in.
	svc := NewWithAgentResolver(foundExecutable("agy"), &switchableProberFake{state: domain.AgentAuthenticationAuthorized}, pty, "")
	t.Cleanup(svc.Close)
	if _, err := svc.StartLogin(t.Context(), "agy", "browser", ""); err == nil {
		t.Fatal("started a guided row on linux")
	}
	started, err := svc.StartLogin(t.Context(), "agy", "native", "")
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(2500 * time.Millisecond)
	current, err := svc.Login(t.Context(), "agy", started.ID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Status != "running" || len(pty.pressed()) != 0 {
		t.Fatalf("snapshot = %+v keys = %v, want native sign-in still running untouched", current, pty.pressed())
	}
	if _, err := svc.CancelLogin(t.Context(), "agy", started.ID); err != nil {
		t.Fatal(err)
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
		{"keeps a complete link", "https://accounts.google.com/o/oauth2/auth?client_id=x\nCopy\n", "https://accounts.google.com/o/oauth2/auth?client_id=x"},
		{"stitches a link wrapped onto two full lines", agyWrappedLink[0] + "\n" + agyWrappedLink[1] + "\n(1–20 of 29 lines)\n", agyWrappedLink[0] + agyWrappedLink[1]},
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
