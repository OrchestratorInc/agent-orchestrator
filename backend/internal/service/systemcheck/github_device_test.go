package systemcheck

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"
)

type deviceRunner struct {
	output  string
	release chan error
	argv    []string
}

func (r *deviceRunner) Run(ctx context.Context, argv []string, stdout, stderr io.Writer) error {
	r.argv = argv
	_, _ = io.WriteString(stderr, r.output)
	select {
	case err := <-r.release:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func newDeviceService(runner *deviceRunner) *Service {
	return NewWithCommandRunner(&fakeHarnessCatalog{}, executableFinderFunc(lookPathFound(map[string]string{"gh": "/usr/bin/gh"})), runner)
}

const deviceOutput = "! First copy your one-time code: ABCD-1234\nOpen this URL to continue in your web browser: https://github.com/login/device\n"

func waitForDeviceState(t *testing.T, svc *Service, want GitHubDeviceLoginState) GitHubDeviceLogin {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, _ := svc.GitHubDeviceLoginStatus(context.Background())
		if got.State == want {
			return got
		}
		time.Sleep(5 * time.Millisecond)
	}
	got, _ := svc.GitHubDeviceLoginStatus(context.Background())
	t.Fatalf("state = %q, want %q", got.State, want)
	return got
}

func TestStartGitHubDeviceLogin_ReturnsCodeWithoutTerminal(t *testing.T) {
	runner := &deviceRunner{output: deviceOutput, release: make(chan error, 1)}
	svc := newDeviceService(runner)

	got, err := svc.StartGitHubDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if got.State != GitHubDeviceLoginAwaitingApproval || got.UserCode != "ABCD-1234" {
		t.Fatalf("login = %+v", got)
	}
	if got.VerificationURL != "https://github.com/login/device" {
		t.Fatalf("url = %q", got.VerificationURL)
	}
	if !strings.Contains(strings.Join(runner.argv, " "), "auth login --web --hostname github.com") {
		t.Fatalf("argv = %v", runner.argv)
	}

	again, err := svc.StartGitHubDeviceLogin(context.Background())
	if err != nil || again.ID != got.ID {
		t.Fatalf("second start should reuse the attempt: %+v err=%v", again, err)
	}

	runner.release <- nil
	waitForDeviceState(t, svc, GitHubDeviceLoginSucceeded)
}

func TestStartGitHubDeviceLogin_ParsesClipboardCodeFormat(t *testing.T) {
	output := "! One-time code (5D0D-70F8) copied to clipboard\nOpen this URL to continue in your web browser: https://github.com/login/device\n"
	runner := &deviceRunner{output: output, release: make(chan error, 1)}
	svc := newDeviceService(runner)

	got, err := svc.StartGitHubDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if got.State != GitHubDeviceLoginAwaitingApproval || got.UserCode != "5D0D-70F8" {
		t.Fatalf("login = %+v", got)
	}
	runner.release <- nil
	waitForDeviceState(t, svc, GitHubDeviceLoginSucceeded)
}

func TestStartGitHubDeviceLogin_FindsCodeByShapeOnly(t *testing.T) {
	// Unknown wording, a code-shaped token inside a link, and a later
	// code-shaped token: only the first one outside a link is the code.
	output := "Visit https://example.com/AAAA-BBBB/x first\nYour code is WXYZ-9876 thanks\nref QQQQ-1111\n"
	runner := &deviceRunner{output: output, release: make(chan error, 1)}
	svc := newDeviceService(runner)

	got, err := svc.StartGitHubDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if got.UserCode != "WXYZ-9876" {
		t.Fatalf("code = %q, want WXYZ-9876", got.UserCode)
	}
	if got.VerificationURL != "https://example.com/AAAA-BBBB/x" {
		t.Fatalf("url = %q", got.VerificationURL)
	}
	runner.release <- nil
	waitForDeviceState(t, svc, GitHubDeviceLoginSucceeded)
}

func TestStartGitHubDeviceLogin_IgnoresColorCodes(t *testing.T) {
	output := "\x1b[0;33m!\x1b[0m One-time code (\x1b[0;1;39m5D0D-70F8\x1b[0m) copied to clipboard\r\n\x1b[0;1;39mOpen this URL\x1b[0m to continue in your web browser: https://github.com/login/device\r\n"
	runner := &deviceRunner{output: output, release: make(chan error, 1)}
	svc := newDeviceService(runner)

	got, err := svc.StartGitHubDeviceLogin(context.Background())
	if err != nil {
		t.Fatalf("start: %v", err)
	}
	if got.UserCode != "5D0D-70F8" || got.VerificationURL != "https://github.com/login/device" {
		t.Fatalf("login = %+v", got)
	}
	runner.release <- nil
	waitForDeviceState(t, svc, GitHubDeviceLoginSucceeded)
}

func TestStartGitHubDeviceLogin_ReportsFailure(t *testing.T) {
	runner := &deviceRunner{output: deviceOutput + "failed to authenticate: expired_token\n", release: make(chan error, 1)}
	svc := newDeviceService(runner)
	if _, err := svc.StartGitHubDeviceLogin(context.Background()); err != nil {
		t.Fatal(err)
	}
	runner.release <- errors.New("exit status 1")
	got := waitForDeviceState(t, svc, GitHubDeviceLoginFailed)
	if !strings.Contains(got.Error, "expired_token") {
		t.Fatalf("error = %q", got.Error)
	}
}

func TestCancelGitHubDeviceLogin(t *testing.T) {
	runner := &deviceRunner{output: deviceOutput, release: make(chan error)}
	svc := newDeviceService(runner)
	first, err := svc.StartGitHubDeviceLogin(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	got, err := svc.CancelGitHubDeviceLogin(context.Background())
	if err != nil || got.State != GitHubDeviceLoginCancelled {
		t.Fatalf("cancel = %+v err=%v", got, err)
	}
	time.Sleep(50 * time.Millisecond)
	if final, _ := svc.GitHubDeviceLoginStatus(context.Background()); final.State != GitHubDeviceLoginCancelled {
		t.Fatalf("cancelled attempt was overwritten: %+v", final)
	}
	next, err := svc.StartGitHubDeviceLogin(context.Background())
	if err != nil || next.State != GitHubDeviceLoginAwaitingApproval || next.ID == first.ID {
		t.Fatalf("restart = %+v err=%v", next, err)
	}
}

func TestGitHubDeviceLoginStatus_IdleByDefault(t *testing.T) {
	got, err := newDeviceService(&deviceRunner{}).GitHubDeviceLoginStatus(context.Background())
	if err != nil || got.State != GitHubDeviceLoginIdle {
		t.Fatalf("status = %+v err=%v", got, err)
	}
}
