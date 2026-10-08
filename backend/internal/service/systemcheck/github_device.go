package systemcheck

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
)

// GitHubDeviceLoginState is where a GitHub CLI device sign-in stands.
type GitHubDeviceLoginState string

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
	githubDeviceCodePattern = regexp.MustCompile(`one-time code:\s*([A-Z0-9]{4}-[A-Z0-9]{4})`)
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
		idx := strings.IndexByte(l.partial, '\n')
		if idx < 0 {
			break
		}
		line := strings.TrimSpace(l.partial[:idx])
		l.partial = l.partial[idx+1:]
		l.consumeLine(line)
	}
	return len(p), nil
}

func (l *githubDeviceLogin) consumeLine(line string) {
	if line == "" {
		return
	}
	l.lastLine = line
	if m := githubDeviceCodePattern.FindStringSubmatch(line); m != nil {
		l.snap.UserCode = m[1]
		if l.snap.State == GitHubDeviceLoginStarting {
			l.snap.State = GitHubDeviceLoginAwaitingApproval
		}
		l.markReady()
		return
	}
	if strings.Contains(line, "URL") {
		if url := githubDeviceURLPattern.FindString(line); strings.HasPrefix(url, "https://") {
			l.snap.VerificationURL = url
		}
	}
}

func (l *githubDeviceLogin) finish(err error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	defer l.markReady()
	if l.snap.State == GitHubDeviceLoginCancelled {
		return
	}
	if err == nil {
		l.snap.State = GitHubDeviceLoginSucceeded
		return
	}
	l.snap.State = GitHubDeviceLoginFailed
	l.snap.Error = deviceFailureMessage(l.lastLine)
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
// code and decides when to open GitHub; nothing here opens a browser.
func (s *Service) StartGitHubDeviceLogin(ctx context.Context) (GitHubDeviceLogin, error) {
	if err := ctx.Err(); err != nil {
		return GitHubDeviceLogin{}, err
	}
	path, err := s.executables.LookPath("gh")
	if err != nil || path == "" {
		return GitHubDeviceLogin{}, apierr.Invalid("GITHUB_CLI_UNAVAILABLE", "GitHub CLI was not found on PATH.", nil)
	}
	if s.commands == nil {
		return GitHubDeviceLogin{}, apierr.Internal("GITHUB_AUTH_UNAVAILABLE", "GitHub authentication is unavailable.")
	}

	s.deviceMu.Lock()
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
	s.deviceMu.Unlock()

	argv := []string{path, "auth", "login", "--web", "--hostname", "github.com", "--git-protocol", "https", "--skip-ssh-key"}
	go func() {
		defer cancel()
		login.finish(s.commands.Run(runCtx, argv, login, login))
	}()
	return s.awaitDeviceCode(ctx, login)
}

func (s *Service) awaitDeviceCode(ctx context.Context, login *githubDeviceLogin) (GitHubDeviceLogin, error) {
	timer := time.NewTimer(githubDeviceCodeWait)
	defer timer.Stop()
	select {
	case <-login.ready:
	case <-timer.C:
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
