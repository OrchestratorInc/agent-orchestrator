package systemcheck

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// GitHubDeviceLoginState is where a GitHub CLI device sign-in stands.
type GitHubDeviceLoginState string

// The states a device sign-in moves through.
const (
	GitHubDeviceLoginIdle             GitHubDeviceLoginState = "idle"
	GitHubDeviceLoginStarting         GitHubDeviceLoginState = "starting"
	GitHubDeviceLoginAwaitingApproval GitHubDeviceLoginState = "awaiting_approval"
	GitHubDeviceLoginSucceeded        GitHubDeviceLoginState = "succeeded"
	GitHubDeviceLoginFailed           GitHubDeviceLoginState = "failed"
	GitHubDeviceLoginCancelled        GitHubDeviceLoginState = "cancelled"
)

const (
	// githubDeviceLoginTimeout bounds the whole sign-in; GitHub expires an
	// unused device code after about 15 minutes.
	githubDeviceLoginTimeout = 15 * time.Minute
	// githubDeviceCodeWait bounds how long Start waits for gh to print the code.
	githubDeviceCodeWait = 10 * time.Second

	githubDefaultVerificationURL = "https://github.com/login/device"
)

var (
	// gh's wording around the code changes between releases ("First copy your
	// one-time code: ABCD-1234", "One-time code (ABCD-1234) copied to
	// clipboard"), so the code is found by its shape alone.
	githubDeviceCodePattern = regexp.MustCompile(`\b([A-Z0-9]{4}-[A-Z0-9]{4})\b`)
	githubDeviceURLPattern  = regexp.MustCompile(`https://\S+`)
)

// GitHubDeviceLogin is a snapshot of the device sign-in. The code is the
// public one-time user code GitHub asks the person to type; it is not a token.
type GitHubDeviceLogin struct {
	ID              string                 `json:"id,omitempty" description:"Identifier of the current sign-in attempt."`
	State           GitHubDeviceLoginState `json:"state" enum:"idle,starting,awaiting_approval,succeeded,failed,cancelled" description:"Where the sign-in stands."`
	UserCode        string                 `json:"userCode,omitempty" description:"One-time code the person enters on GitHub."`
	VerificationURL string                 `json:"verificationUrl,omitempty" description:"GitHub page where the code is entered."`
	Error           string                 `json:"error,omitempty" description:"Why the sign-in failed, when it did."`
}

type githubDeviceLogin struct {
	mu     sync.Mutex
	snap   GitHubDeviceLogin
	cancel context.CancelFunc
	// ready closes once the code is known or the attempt has ended.
	ready     chan struct{}
	readyOnce sync.Once
	lastLine  string
	partial   string
}

func (l *githubDeviceLogin) markReady() { l.readyOnce.Do(func() { close(l.ready) }) }

func (l *githubDeviceLogin) snapshot() GitHubDeviceLogin {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.snap
}

// Write parses gh's output line by line. gh prints the code and URL on
// stderr when it runs without a terminal.
func (l *githubDeviceLogin) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.partial += string(p)
	for {
		idx := strings.IndexAny(l.partial, "\r\n")
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(ansi.Strip(l.partial[:idx]))
		l.partial = l.partial[idx+1:]
		l.consumeLine(line)
	}
	// Some releases/wrappers write prompts without a trailing newline.
	// Keep incomplete escape sequences and code fragments for the next write.
	const maxPartialBytes = 16 * 1024
	if len(l.partial) > maxPartialBytes {
		l.partial = l.partial[len(l.partial)-maxPartialBytes:]
	}
	l.consumeLine(strings.TrimSpace(ansi.Strip(l.partial)))
	return len(p), nil
}

func (l *githubDeviceLogin) consumeLine(line string) {
	if line == "" {
		return
	}
	l.lastLine = line
	// Login is pinned to github.com. Debug output can include OAuth API URLs
	// before the actual verification link, so retain the known browser URL.
	// The first code-shaped token outside a link is the code; once it is known,
	// nothing later replaces it.
	if l.snap.UserCode != "" {
		return
	}
	if m := githubDeviceCodePattern.FindStringSubmatch(githubDeviceURLPattern.ReplaceAllString(line, "")); m != nil {
		l.snap.UserCode = m[1]
		if l.snap.State == GitHubDeviceLoginStarting {
			l.snap.State = GitHubDeviceLoginAwaitingApproval
		}
		l.markReady()
	}
}

func (l *githubDeviceLogin) finish(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	defer l.markReady()
	if l.snap.State == GitHubDeviceLoginCancelled || l.snap.State == GitHubDeviceLoginFailed {
		return
	}
	if err == nil {
		l.snap.State = GitHubDeviceLoginSucceeded
		return
	}
	l.snap.State = GitHubDeviceLoginFailed
	if errors.Is(err, context.DeadlineExceeded) {
		l.snap.Error = "GitHub sign-in timed out. Try again."
	} else {
		l.snap.Error = deviceFailureMessage(l.lastLine)
	}
}

func deviceFailureMessage(last string) string {
	const fallback = "GitHub sign-in did not complete."
	last = strings.TrimSpace(last)
	if last == "" {
		return fallback
	}
	if len(last) > 200 {
		last = last[:200]
	}
	return last
}

// StartGitHubDeviceLogin begins (or returns the in-flight) GitHub CLI device
// sign-in. It runs gh without a terminal, so gh skips every prompt, prints the
// one-time code and starts waiting for approval at once. The caller shows the
// code and opens GitHub in the browser.
func (s *Service) StartGitHubDeviceLogin(ctx context.Context) (GitHubDeviceLogin, error) {
	if err := ctx.Err(); err != nil {
		return GitHubDeviceLogin{}, err
	}
	path, err := s.executables.LookPath("gh")
	if err != nil || path == "" {
		return GitHubDeviceLogin{}, apierr.Invalid("GITHUB_CLI_UNAVAILABLE", "GitHub CLI was not found on PATH.", nil)
	}
	runner, ok := s.commands.(ports.GitHubDeviceLoginRunner)
	if !ok {
		return GitHubDeviceLogin{}, apierr.Internal("GITHUB_AUTH_UNAVAILABLE", "GitHub authentication is unavailable.")
	}

	if err := ctx.Err(); err != nil {
		return GitHubDeviceLogin{}, err
	}
	s.deviceMu.Lock()
	if s.deviceClosed {
		s.deviceMu.Unlock()
		return GitHubDeviceLogin{}, apierr.Internal("GITHUB_AUTH_UNAVAILABLE", "GitHub authentication is shutting down.")
	}
	if cur := s.device; cur != nil {
		if state := cur.snapshot().State; state == GitHubDeviceLoginStarting || state == GitHubDeviceLoginAwaitingApproval {
			s.deviceMu.Unlock()
			return s.awaitDeviceCode(ctx, cur)
		}
	}
	login := &githubDeviceLogin{
		snap:  GitHubDeviceLogin{ID: uuid.NewString(), State: GitHubDeviceLoginStarting, VerificationURL: githubDefaultVerificationURL},
		ready: make(chan struct{}),
	}
	// The sign-in outlives the request that started it.
	runCtx, cancel := context.WithTimeout(context.Background(), githubDeviceLoginTimeout)
	login.cancel = cancel
	s.device = login
	s.deviceRuns.Add(1)
	s.deviceMu.Unlock()

	codeWait := s.deviceCodeWait
	if codeWait <= 0 {
		codeWait = githubDeviceCodeWait
	}
	go func() {
		timer := time.NewTimer(codeWait)
		defer timer.Stop()
		select {
		case <-login.ready:
			return
		case <-timer.C:
			login.mu.Lock()
			if login.snap.State != GitHubDeviceLoginStarting {
				login.mu.Unlock()
				return
			}
			login.snap.State = GitHubDeviceLoginFailed
			login.snap.Error = "GitHub CLI did not provide a sign-in code. Update GitHub CLI and try again."
			login.mu.Unlock()
			cancel()
			login.markReady()
		}
	}()
	go func() {
		defer s.deviceRuns.Done()
		defer cancel()
		login.finish(runner.RunGitHubDeviceLogin(runCtx, path, login, login))
	}()
	return s.awaitDeviceCode(ctx, login)
}

func (s *Service) awaitDeviceCode(ctx context.Context, login *githubDeviceLogin) (GitHubDeviceLogin, error) {
	select {
	case <-login.ready:
	case <-ctx.Done():
		return GitHubDeviceLogin{}, ctx.Err()
	}
	return login.snapshot(), nil
}

// GitHubDeviceLoginStatus reports the current sign-in attempt, or idle.
func (s *Service) GitHubDeviceLoginStatus(ctx context.Context) (GitHubDeviceLogin, error) {
	if err := ctx.Err(); err != nil {
		return GitHubDeviceLogin{}, err
	}
	s.deviceMu.Lock()
	cur := s.device
	s.deviceMu.Unlock()
	if cur == nil {
		return GitHubDeviceLogin{State: GitHubDeviceLoginIdle}, nil
	}
	return cur.snapshot(), nil
}

// CancelGitHubDeviceLogin stops an in-flight sign-in and reports the result.
func (s *Service) CancelGitHubDeviceLogin(ctx context.Context) (GitHubDeviceLogin, error) {
	if err := ctx.Err(); err != nil {
		return GitHubDeviceLogin{}, err
	}
	s.deviceMu.Lock()
	cur := s.device
	s.deviceMu.Unlock()
	if cur == nil {
		return GitHubDeviceLogin{State: GitHubDeviceLoginIdle}, nil
	}
	cur.mu.Lock()
	active := cur.snap.State == GitHubDeviceLoginStarting || cur.snap.State == GitHubDeviceLoginAwaitingApproval
	if active {
		cur.snap.State = GitHubDeviceLoginCancelled
	}
	cur.mu.Unlock()
	if active {
		cur.cancel()
		cur.markReady()
	}
	return cur.snapshot(), nil
}

// Close stops authentication work when the owning daemon shuts down.
func (s *Service) Close() {
	s.deviceMu.Lock()
	s.deviceClosed = true
	s.deviceMu.Unlock()
	_, _ = s.CancelGitHubDeviceLogin(context.Background())
	drained := make(chan struct{})
	go func() { s.deviceRuns.Wait(); close(drained) }()
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
	}
}
