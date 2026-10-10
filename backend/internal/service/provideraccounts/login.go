package provideraccounts

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// LoginCoordinator keeps login state in memory, but never holds its global
// lock while talking to the helper, a provider, or SQLite. A slow login must
// not freeze another provider's login or the cancel/status buttons.
type LoginCoordinator struct {
	mu           sync.Mutex
	accounts     *Service
	proxy        ports.ProviderAccountLoginProxy
	attempts     map[string]ports.ProviderLogin
	attemptLocks map[string]chan struct{}
	starts       map[string]chan struct{}
	newID        func() string
	expires      map[string]time.Time
	now          func() time.Time
}

// NewLoginCoordinator creates the daemon-owned login attempt coordinator.
func NewLoginCoordinator(accounts *Service, proxy ports.ProviderAccountLoginProxy, newID func() string) *LoginCoordinator {
	return &LoginCoordinator{
		accounts: accounts, proxy: proxy, attempts: make(map[string]ports.ProviderLogin),
		attemptLocks: make(map[string]chan struct{}), starts: map[string]chan struct{}{"codex": make(chan struct{}, 1), "claude": make(chan struct{}, 1)},
		newID: newID, expires: make(map[string]time.Time), now: time.Now,
	}
}

// Start starts or resumes a provider login attempt.
func (l *LoginCoordinator) Start(ctx context.Context, provider, accountID string) (ports.ProviderLogin, error) {
	return l.StartRequest(ctx, provider, accountID, "browser", ports.ProviderLoginInput{})
}

// StartRequest starts one of the supported credential acquisition methods.
// Browser login remains the default and keeps the original public contract.
func (l *LoginCoordinator) StartRequest(ctx context.Context, provider, accountID, mode string, input ports.ProviderLoginInput) (ports.ProviderLogin, error) {
	if provider != "codex" && provider != "claude" {
		return ports.ProviderLogin{}, ports.ErrProviderAccountIncompatible
	}

	ctx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	select {
	case l.starts[provider] <- struct{}{}:
		defer func() { <-l.starts[provider] }()
	case <-ctx.Done():
		return ports.ProviderLogin{}, ctx.Err()
	}
	var waitingID string
	l.mu.Lock()
	for _, login := range l.attempts {
		if login.Provider == provider && login.Status == "waiting" {
			waitingID = login.ID
			break
		}
	}
	l.mu.Unlock()
	if waitingID != "" {
		current, err := l.Status(ctx, waitingID)
		if err != nil {
			return current, err
		}
		if current.Status == "waiting" {
			if current.AccountID == accountID && (current.Mode == mode || current.Mode == "" && mode == "browser") {
				return current, nil
			}
			return ports.ProviderLogin{}, fmt.Errorf("a login is already in progress for this provider: %w", ports.ErrProviderAccountConflict)
		}
	}

	if accountID != "" {
		state, err := l.accounts.State(ctx)
		if err != nil {
			return ports.ProviderLogin{}, err
		}
		a, ok := account(state, accountID)
		if !ok {
			return ports.ProviderLogin{}, ports.ErrProviderAccountUnknown
		}
		if a.Provider != provider {
			return ports.ProviderLogin{}, ports.ErrProviderAccountIncompatible
		}
		// An account whose saved sign-in CLIProxy no longer accepts, or that
		// has stopped renewing, signs in again like a signed-out one.
		if a.CredentialRef != "" && !l.accounts.signInReplaceable(a) {
			return ports.ProviderLogin{}, fmt.Errorf("account is already signed in: %w", ports.ErrProviderAccountConflict)
		}
	}

	l.mu.Lock()
	loginID := l.newID()
	l.mu.Unlock()
	var login ports.ProviderLogin
	var err error
	if mode == "" || mode == "browser" {
		login, err = l.proxy.StartAccountLogin(ctx, provider, loginID)
	} else if modes, ok := l.proxy.(ports.ProviderAccountLoginModes); ok {
		login, err = modes.StartAccountLoginMode(ctx, provider, loginID, mode, input)
	} else {
		return ports.ProviderLogin{}, fmt.Errorf("login mode %q is unavailable: %w", mode, ports.ErrProviderAccountIncompatible)
	}
	if err != nil {
		return login, err
	}
	login.AccountID = accountID
	if login.Mode == "" {
		login.Mode = mode
	}
	if login.Status == "" {
		login.Status = "waiting"
	}

	l.mu.Lock()
	l.attempts[login.ID] = login
	l.attemptLocks[login.ID] = make(chan struct{}, 1)
	duration := 6 * time.Minute
	if login.ExpiresIn > 0 && login.ExpiresIn <= 900 {
		duration = time.Duration(login.ExpiresIn) * time.Second
	}
	l.expires[login.ID] = l.now().Add(duration)
	l.mu.Unlock()
	return login, nil
}

// Status polls a login without holding the global coordinator lock.
func (l *LoginCoordinator) Status(ctx context.Context, id string) (ports.ProviderLogin, error) {
	lock, ok := l.attemptLock(id)
	if !ok {
		return ports.ProviderLogin{}, ports.ErrProviderLoginUnknown
	}
	ctx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return ports.ProviderLogin{}, ctx.Err()
	}
	l.mu.Lock()
	login, ok := l.attempts[id]
	l.mu.Unlock()
	if !ok {
		return ports.ProviderLogin{}, ports.ErrProviderLoginUnknown
	}
	return l.status(ctx, login)
}

// status must be called with the attempt lock held.
func (l *LoginCoordinator) status(ctx context.Context, login ports.ProviderLogin) (ports.ProviderLogin, error) {
	if login.Status != "waiting" {
		return login, nil
	}
	status, err := l.proxy.AccountLoginStatus(ctx, login)
	if err != nil {
		// A provider outage must not turn a six-minute login into an immortal
		// spinner. Once the deadline has passed, expose a terminal state.
		if l.expired(login.ID) {
			return l.expire(login)
		}
		return login, err
	}
	if status == "complete" {
		verified, err := l.proxy.VerifiedAccountLogin(ctx, login.ID)
		if err != nil {
			return login, err
		}
		if verified.Provider != login.Provider {
			login.Status = "failed"
			l.save(login)
			return login, errors.Join(ports.ErrProviderAccountIncompatible, l.accounts.discardLogin(ctx, verified))
		}
		accountID, err := l.accounts.RecordCredential(ctx, verified, login.AccountID)
		if err != nil {
			if errors.Is(err, ports.ErrProviderAccountIncompatible) || errors.Is(err, ports.ErrProviderAccountConflict) || errors.Is(err, ports.ErrProviderAccountUnknown) {
				login.Status = "failed"
				l.save(login)
				err = errors.Join(err, l.accounts.discardLogin(ctx, verified))
			}
			return login, err
		}
		login.AccountID = accountID
	}
	if status == "waiting" && l.expired(login.ID) {
		return l.expire(login)
	}
	login.Status = status
	l.save(login)
	return login, nil
}

// Cancel cancels a pending attempt without holding the global lock during I/O.
func (l *LoginCoordinator) Cancel(ctx context.Context, id string) error {
	lock, ok := l.attemptLock(id)
	if !ok {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	select {
	case lock <- struct{}{}:
		defer func() { <-lock }()
	case <-ctx.Done():
		return ctx.Err()
	}
	l.mu.Lock()
	login, ok := l.attempts[id]
	l.mu.Unlock()
	if !ok || login.Status != "waiting" {
		return nil
	}
	if err := l.proxy.CancelAccountLogin(ctx, login); err != nil {
		return err
	}
	login.Status = "cancelled"
	l.save(login)
	return l.discardFinished(ctx, login)
}

// discardFinished removes the sign-in an attempt saved before it was cancelled
// or ran out of time. The provider can grant a sign-in a moment before either
// happens; with the attempt over, nothing would ever record that credential.
func (l *LoginCoordinator) discardFinished(ctx context.Context, login ports.ProviderLogin) error {
	verified, err := l.proxy.VerifiedAccountLogin(ctx, login.ID)
	if err != nil {
		// Nothing was saved, or the helper cannot say; the sweep for leftover
		// credentials covers the second case.
		return nil
	}
	return l.accounts.discardLogin(ctx, verified)
}

func (l *LoginCoordinator) attemptLock(id string) (chan struct{}, bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	lock, ok := l.attemptLocks[id]
	return lock, ok
}

func (l *LoginCoordinator) save(login ports.ProviderLogin) {
	l.mu.Lock()
	l.attempts[login.ID] = login
	l.mu.Unlock()
}

func (l *LoginCoordinator) expired(id string) bool {
	l.mu.Lock()
	deadline := l.expires[id]
	now := l.now()
	l.mu.Unlock()
	return !now.Before(deadline)
}

func (l *LoginCoordinator) expire(login ports.ProviderLogin) (ports.ProviderLogin, error) {
	// Cancellation is best effort. The important part is returning a terminal
	// state so the UI can stop polling and offer a fresh login. If upstream
	// refuses cancellation, retain waiting state so a later poll can retry it.
	cancelCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	err := l.proxy.CancelAccountLogin(cancelCtx, login)
	cancel()
	if err != nil {
		return login, err
	}
	login.Status = "failed"
	l.save(login)
	discardCtx, cancelDiscard := context.WithTimeout(context.Background(), providerAccountOperationTimeout)
	defer cancelDiscard()
	return login, l.discardFinished(discardCtx, login)
}
