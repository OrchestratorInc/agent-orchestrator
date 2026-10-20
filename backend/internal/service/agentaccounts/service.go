// Package agentaccounts manages the Claude Code and Codex subscription accounts
// AO launches agents with. An account is a config folder of the provider's own
// CLI; the computer's own folder is the "This computer" account. A session is
// pinned to one account per provider and moves by restarting its agent there.
package agentaccounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

// freshFor is how long a read of an account's identity and limits is reused.
const freshFor = 5 * time.Minute

// retryEmptyAfter is how long an answer with no limit windows is reused. A
// folder read just after its sign-in can answer before its limits are known.
const retryEmptyAfter = 30 * time.Second

// Terminals opens the embedded terminal a sign-in runs in.
type Terminals interface {
	OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error)
	CloseShellTerminal(context.Context, string) error
}

// resetCrediter is the Codex-only ability to spend a usage-limit reset.
type resetCrediter interface {
	ConsumeResetCredit(ctx context.Context, home, idempotencyKey string) (domain.CodexResetCreditOutcome, error)
}

// Service is the account catalog, its cached readings, and the per-session
// launch environment.
type Service struct {
	store     ports.AgentAccountStore
	providers map[domain.AgentAccountProvider]ports.AgentAccountProviderCLI
	root      string // account folders live in root/<provider>/<id>
	terminals Terminals
	// stopAgents stops every running agent on an account before it is removed.
	stopAgents func(context.Context, string) error
	log        *slog.Logger
	now        func() time.Time

	mu       sync.Mutex
	readings map[string]*reading
	logins   map[string]*login
	// removing holds accounts being removed. No launch resolves to one, so an
	// agent cannot start on a login AO is about to delete.
	removing       map[string]bool
	defaultChanged func(domain.AgentAccountProvider)
}

// reading is the last identity and limits read for one account.
type reading struct {
	identity    domain.AgentAccountIdentityCheck
	identityErr string
	// mismatch: the folder is signed in, but as another login than its own.
	mismatch  bool
	limits    *domain.AgentAccountLimits
	limitsErr string
	at        time.Time
}

// knownUnusable reports a reading that shows the folder cannot launch as its
// account: logged in by no method at all, or signed in as another login. An
// API key is not a subscription, but the CLI launches with it, so it passes. A
// failed probe shows nothing, and no reading yet is not known.
func (r *reading) knownUnusable() bool {
	return r != nil && (r.mismatch || (r.identityErr == "" && !r.identity.LoggedIn))
}

type login struct {
	id       string
	provider domain.AgentAccountProvider
	home     string
	terminal string
	// account is the saved account a sign-in again runs in; nil for a new one.
	account *domain.AgentAccount
}

// New builds the service. root must be absolute; the account folders' paths
// are stored exactly, because Claude keys each folder's login on the path text.
func New(store ports.AgentAccountStore, providers []ports.AgentAccountProviderCLI, root string, terminals Terminals, log *slog.Logger) *Service {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	byProvider := map[domain.AgentAccountProvider]ports.AgentAccountProviderCLI{}
	for _, p := range providers {
		byProvider[p.Provider()] = p
	}
	return &Service{
		store: store, providers: byProvider, root: filepath.Clean(root), terminals: terminals, log: log,
		now: func() time.Time { return time.Now().UTC() }, readings: map[string]*reading{}, logins: map[string]*login{}, removing: map[string]bool{},
	}
}

// SetTerminals late-binds the terminal sign-ins run in, which the daemon builds
// after Session Manager.
func (s *Service) SetTerminals(terminals Terminals) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminals = terminals
}

// SetAgentStopper late-binds what stops the agents on an account before it is
// removed, so none keeps running on a login AO deletes.
func (s *Service) SetAgentStopper(stop func(ctx context.Context, accountID string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopAgents = stop
}

// AccountView is an account with its latest readings, as the app shows it.
type AccountView struct {
	Account     domain.AgentAccount
	Default     bool
	SignedIn    bool
	IdentityErr string
	Limits      *domain.AgentAccountLimits
	LimitsErr   string
}

// List returns every account, the computer's own first per provider. refresh
// reads identity and limits again even when the last reading is fresh.
func (s *Service) List(ctx context.Context, refresh bool) ([]AccountView, error) {
	accounts, err := s.accounts(ctx)
	if err != nil {
		return nil, err
	}
	var wg sync.WaitGroup
	for _, account := range accounts {
		if s.needsReading(account.ID, refresh) {
			wg.Go(func() { s.read(ctx, account) })
		}
	}
	wg.Wait()
	views := make([]AccountView, 0, len(accounts))
	for _, account := range accounts {
		def, _ := s.defaultID(ctx, account.Provider)
		s.mu.Lock()
		r := s.readings[account.ID]
		s.mu.Unlock()
		view := AccountView{Account: account, Default: def == account.ID}
		if r != nil {
			view.SignedIn, view.IdentityErr, view.Limits, view.LimitsErr = r.identity.SignedIn, r.identityErr, r.limits, r.limitsErr
			if account.Device() {
				view.Account.Email, view.Account.Plan = r.identity.Email, r.identity.Plan
			} else if r.identity.Plan != "" {
				view.Account.Plan = r.identity.Plan
			}
		}
		views = append(views, view)
	}
	return views, nil
}

func (s *Service) needsReading(id string, refresh bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.readings[id]
	if refresh || r == nil {
		return true
	}
	ttl := freshFor
	if r.limits != nil && len(r.limits.Windows) == 0 {
		// A plan that never reports windows is then read on every poll, one CLI call a minute.
		ttl = retryEmptyAfter
	}
	return s.now().Sub(r.at) > ttl
}

// read asks the provider's CLI for the account's identity and limits.
func (s *Service) read(ctx context.Context, account domain.AgentAccount) *reading {
	provider := s.providers[account.Provider]
	if provider == nil {
		return nil
	}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	r := &reading{at: s.now()}
	identity, err := provider.Identity(ctx, account.Home)
	r.identity = identity
	if err != nil {
		r.identityErr = err.Error()
		// A failed check (a timeout under load) says nothing about the login:
		// keep what the last good one found, so it does not read as signed out.
		s.mu.Lock()
		if prev := s.readings[account.ID]; prev != nil && prev.identityErr == "" {
			r.identity, r.limits = prev.identity, prev.limits
		}
		s.mu.Unlock()
	}
	switch {
	case err != nil:
	case !identity.SignedIn:
		r.limitsErr = "signed out"
	case !account.Device() && account.Email != "" && !strings.EqualFold(identity.Email, account.Email):
		// The right-account check: the folder must still hold the login it was added with.
		r.identity.SignedIn = false
		r.mismatch = true
		r.identityErr = fmt.Sprintf("the folder is now signed in as %s, not %s", identity.Email, account.Email)
	default:
		if limits, limitsErr := provider.Limits(ctx, account.Home); limitsErr != nil {
			r.limitsErr = limitsErr.Error()
		} else {
			r.limits = &limits
		}
	}
	// A caller that went away (a closed request, a cancelled move) says nothing
	// about the account; caching its failure would refuse the account for the
	// rest of the cache window.
	if caller.Err() != nil {
		return r
	}
	s.mu.Lock()
	s.readings[account.ID] = r
	s.mu.Unlock()
	return r
}

// accounts is the computer's own account per provider, then the saved ones.
func (s *Service) accounts(ctx context.Context) ([]domain.AgentAccount, error) {
	saved, err := s.store.ListAgentAccounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]domain.AgentAccount, 0, len(saved)+len(domain.AgentAccountProviders))
	for _, p := range domain.AgentAccountProviders {
		if s.providers[p] == nil {
			continue
		}
		out = append(out, domain.AgentAccount{ID: domain.DeviceAgentAccountID(p), Provider: p})
		for _, account := range saved {
			if account.Provider == p {
				out = append(out, account)
			}
		}
	}
	return out, nil
}

func (s *Service) find(ctx context.Context, id string) (domain.AgentAccount, error) {
	accounts, err := s.accounts(ctx)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	for _, account := range accounts {
		if account.ID == id {
			return account, nil
		}
	}
	return domain.AgentAccount{}, ports.ErrAgentAccountUnknown
}

// defaultID is the provider's account for new sessions; the computer's own
// when none was picked or the picked one was removed.
func (s *Service) defaultID(ctx context.Context, p domain.AgentAccountProvider) (string, error) {
	id, ok, err := s.store.GetAgentAccountDefault(ctx, p)
	if err != nil || !ok {
		return domain.DeviceAgentAccountID(p), err
	}
	switch _, err := s.find(ctx, id); {
	case errors.Is(err, ports.ErrAgentAccountUnknown) || s.beingRemoved(id):
		return domain.DeviceAgentAccountID(p), nil // the picked default was removed
	case err != nil:
		return "", err
	}
	return id, nil
}

func (s *Service) beingRemoved(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.removing[id]
}

// DefaultSignIn reports whether the saved account new sessions of harness start
// on is signed in; handled is false when they start on the computer's own
// login, whose readiness the CLI reports itself. A reading older than the cache
// window is read again, so readiness polling costs one CLI call per window.
func (s *Service) DefaultSignIn(ctx context.Context, harness domain.AgentHarness) (signedIn, handled bool) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return false, false
	}
	def, err := s.defaultID(ctx, p)
	if err != nil || def == domain.DeviceAgentAccountID(p) {
		return false, false
	}
	account, err := s.find(ctx, def)
	if err != nil {
		return false, false
	}
	s.mu.Lock()
	r := s.readings[account.ID]
	s.mu.Unlock()
	if r == nil || s.now().Sub(r.at) > freshFor {
		s.read(ctx, account)
		s.mu.Lock()
		r = s.readings[account.ID]
		s.mu.Unlock()
	}
	if r == nil || (r.identityErr != "" && !r.mismatch) {
		return false, false // not known: leave it to the CLI's own readiness
	}
	return r.identity.SignedIn, true
}

// SetDefaultChanged late-binds what must hear that the account new sessions of
// a provider start on, or its sign-in, may have changed (agent readiness).
func (s *Service) SetDefaultChanged(changed func(domain.AgentAccountProvider)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultChanged = changed
}

func (s *Service) notifyDefaultChanged(p domain.AgentAccountProvider) {
	s.mu.Lock()
	changed := s.defaultChanged
	s.mu.Unlock()
	if changed != nil {
		changed(p)
	}
}

// RunsOnComputerLogin reports whether a new session of harness asking for
// accountID ("" for the default) would launch on the computer's own login.
// Harnesses without accounts always do.
func (s *Service) RunsOnComputerLogin(ctx context.Context, harness domain.AgentHarness, accountID string) bool {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return true
	}
	if accountID == "" {
		def, err := s.defaultID(ctx, p)
		if err != nil {
			return true
		}
		accountID = def
	}
	return accountID == domain.DeviceAgentAccountID(p)
}

// SetDefault makes accountID the account new sessions of its provider start on.
// Running sessions and the computer's own CLI are not touched.
func (s *Service) SetDefault(ctx context.Context, accountID string) error {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.store.SetAgentAccountDefault(ctx, account.Provider, account.ID); err != nil {
		return err
	}
	s.notifyDefaultChanged(account.Provider)
	return nil
}

// LoginStart is a sign-in running in an embedded terminal.
type LoginStart struct {
	OperationID string
	Provider    domain.AgentAccountProvider
	Terminal    shellterm.ShellTerminal
}

// StartLogin creates a new account folder and runs the provider's own sign-in
// for it in a terminal.
func (s *Service) StartLogin(ctx context.Context, p domain.AgentAccountProvider) (LoginStart, error) {
	return s.startLogin(ctx, p, nil)
}

// StartSignInAgain runs the provider's sign-in in a saved account's own folder,
// so a signed-out account comes back with its sessions, history and settings.
func (s *Service) StartSignInAgain(ctx context.Context, accountID string) (LoginStart, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return LoginStart{}, err
	}
	if account.Device() {
		return LoginStart{}, ports.ErrAgentAccountDeviceFixed
	}
	return s.startLogin(ctx, account.Provider, &account)
}

func (s *Service) startLogin(ctx context.Context, p domain.AgentAccountProvider, account *domain.AgentAccount) (LoginStart, error) {
	provider := s.providers[p]
	s.mu.Lock()
	terminals := s.terminals
	s.mu.Unlock()
	if provider == nil || terminals == nil {
		return LoginStart{}, ports.ErrAgentAccountProviderMismatch
	}
	id := uuid.NewString()
	home := filepath.Join(s.root, string(p), id)
	title := map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "Add Claude account", domain.AgentAccountProviderCodex: "Add Codex account"}[p]
	if account != nil {
		home = account.Home
		title = map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "Sign in to Claude account", domain.AgentAccountProviderCodex: "Sign in to Codex account"}[p]
	}
	// A saved account's folder is never removed here; only a new one is.
	discard := func() {
		if account == nil {
			_ = os.RemoveAll(home)
		}
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return LoginStart{}, fmt.Errorf("create account folder: %w", err)
	}
	// Linking first means the sign-in never creates a private copy of a shared folder.
	if err := s.prepare(provider, home); err != nil {
		discard()
		return LoginStart{}, err
	}
	argv, err := provider.LoginArgv(ctx)
	if err != nil {
		discard()
		return LoginStart{}, err
	}
	terminal, err := terminals.OpenCommandTerminal(ctx, shellterm.OpenCommandTerminalInput{Argv: argv, Env: provider.LaunchEnv(home), WorkingDir: home, Title: title})
	if err != nil {
		discard()
		return LoginStart{}, fmt.Errorf("open sign-in terminal: %w", err)
	}
	s.mu.Lock()
	s.logins[id] = &login{id: id, provider: p, home: home, terminal: terminal.HandleID, account: account}
	s.mu.Unlock()
	return LoginStart{OperationID: id, Provider: p, Terminal: terminal}, nil
}

// LoginStatus is how a sign-in stands.
type LoginStatus string

// Sign-in states.
const (
	LoginWaiting   LoginStatus = "waiting"
	LoginComplete  LoginStatus = "complete"
	LoginDuplicate LoginStatus = "duplicate"
	// LoginMismatch is a sign-in again with another login than the account's.
	LoginMismatch LoginStatus = "mismatch"
)

// VerifyLogin checks whether the sign-in finished, and saves the account when
// it did. A login that is already an account, the computer's own included, is
// refused so one login never sits in two folders, each renewing its own token.
func (s *Service) VerifyLogin(ctx context.Context, operationID string) (LoginStatus, *AccountView, error) {
	s.mu.Lock()
	op := s.logins[operationID]
	s.mu.Unlock()
	if op == nil {
		return "", nil, ports.ErrAgentAccountLoginUnknown
	}
	provider := s.providers[op.provider]
	identity, err := provider.Identity(ctx, op.home)
	if err != nil {
		return "", nil, fmt.Errorf("check sign-in: %w", err)
	}
	if !identity.SignedIn {
		return LoginWaiting, nil, nil
	}
	views, err := s.List(ctx, false)
	if err != nil {
		return "", nil, err
	}
	// Claim the sign-in before acting on it. A cancel, or a second check, that
	// ran during the CLI calls above has already ended it, and saving now would
	// keep an account whose folder is gone.
	if !s.claimLogin(op) {
		return "", nil, ports.ErrAgentAccountLoginUnknown
	}
	for _, view := range views {
		if op.account != nil && view.Account.ID == op.account.ID {
			continue
		}
		if view.Account.Provider == op.provider && view.SignedIn && strings.EqualFold(view.Account.Email, identity.Email) {
			s.endLogin(ctx, op, true)
			return LoginDuplicate, &view, nil
		}
	}
	if op.account != nil {
		return s.finishSignInAgain(ctx, op, identity, views)
	}
	account := domain.AgentAccount{ID: op.id, Provider: op.provider, Home: op.home, Email: identity.Email, Plan: identity.Plan, CreatedAt: s.now()}
	if err := s.store.CreateAgentAccount(ctx, account); err != nil {
		s.releaseLogin(op) // still pending: a later check can save it
		return "", nil, err
	}
	if err := provider.Prepare(op.home); err != nil {
		s.log.Warn("prepare account folder", "account", op.id, "error", err)
	}
	s.endLogin(ctx, op, false)
	s.read(ctx, account)
	s.notifyDefaultChanged(account.Provider)
	views, err = s.List(ctx, false)
	if err != nil {
		return "", nil, err
	}
	for _, view := range views {
		if view.Account.ID == account.ID {
			return LoginComplete, &view, nil
		}
	}
	return LoginComplete, nil, nil
}

// claimLogin takes op out of the pending sign-ins if it is still there, so
// exactly one caller goes on to save, refuse or cancel it.
func (s *Service) claimLogin(op *login) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logins[op.id] != op {
		return false
	}
	delete(s.logins, op.id)
	return true
}

// releaseLogin puts a claimed sign-in back after a failure that left it intact.
func (s *Service) releaseLogin(op *login) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins[op.id] = op
}

// finishSignInAgain keeps a saved account's folder signed in only as the login
// it was saved with: its sessions and limits belong to that login.
func (s *Service) finishSignInAgain(ctx context.Context, op *login, identity domain.AgentAccountIdentityCheck, views []AccountView) (LoginStatus, *AccountView, error) {
	var saved *AccountView
	for i := range views {
		if views[i].Account.ID == op.account.ID {
			saved = &views[i]
		}
	}
	if op.account.Email != "" && !strings.EqualFold(op.account.Email, identity.Email) {
		s.endLogin(ctx, op, true)
		return LoginMismatch, saved, nil
	}
	s.endLogin(ctx, op, false)
	s.read(ctx, *op.account)
	s.notifyDefaultChanged(op.account.Provider)
	views, err := s.List(ctx, false)
	if err != nil {
		return "", nil, err
	}
	for _, view := range views {
		if view.Account.ID == op.account.ID {
			return LoginComplete, &view, nil
		}
	}
	return LoginComplete, nil, nil
}

// CancelLogin stops a sign-in and removes its unsaved folder. A saved
// account's folder stays as it is.
func (s *Service) CancelLogin(ctx context.Context, operationID string) error {
	s.mu.Lock()
	op := s.logins[operationID]
	s.mu.Unlock()
	if op == nil || !s.claimLogin(op) {
		return ports.ErrAgentAccountLoginUnknown
	}
	s.endLogin(ctx, op, op.account == nil)
	return nil
}

// endLogin closes a sign-in. discard signs its folder out, so a refused login's
// token does not outlive the attempt, and removes the folder unless it belongs
// to a saved account.
func (s *Service) endLogin(ctx context.Context, op *login, discard bool) {
	s.mu.Lock()
	delete(s.logins, op.id)
	s.mu.Unlock()
	s.mu.Lock()
	terminals := s.terminals
	s.mu.Unlock()
	if op.terminal != "" && terminals != nil {
		_ = terminals.CloseShellTerminal(context.WithoutCancel(ctx), op.terminal)
	}
	if discard {
		// A refused duplicate is signed in; signing out first keeps its token
		// from outliving the folder in the Keychain.
		logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if err := s.providers[op.provider].Logout(logoutCtx, op.home); err != nil {
			s.log.Debug("sign out refused account folder", "provider", op.provider, "error", err)
		}
		if op.account == nil {
			s.removeFolder(op.home)
		}
	}
}

// removeFolder deletes an account folder AO made. Links inside it are removed,
// never followed, so the computer's own history is untouched.
func (s *Service) removeFolder(home string) {
	// The root may now be reached through another spelling (a symlinked data
	// directory) than when the account was made; compare where both resolve.
	root, home2 := s.root, home
	if r, err := filepath.EvalSymlinks(root); err == nil {
		root = r
	}
	if h, err := filepath.EvalSymlinks(home); err == nil {
		home2 = h
	}
	rel, err := filepath.Rel(root, home2)
	if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return
	}
	_ = os.RemoveAll(home)
}

// Remove signs an account out through its CLI and deletes its folder. Sessions
// on it start on the provider's default at their next launch.
func (s *Service) Remove(ctx context.Context, accountID string) error {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Device() {
		return ports.ErrAgentAccountDeviceFixed
	}
	// From here no launch resolves to the account; then its agents stop, so
	// none runs on a login AO deletes. An agent that cannot be stopped keeps the
	// account, usable again.
	s.mu.Lock()
	s.removing[accountID] = true
	stop := s.stopAgents
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.removing, accountID)
		s.mu.Unlock()
	}()
	if stop != nil {
		if err := stop(ctx, accountID); err != nil {
			return fmt.Errorf("%w: %w", ports.ErrAgentAccountInUse, err)
		}
	}
	if err := s.providers[account.Provider].Logout(ctx, account.Home); err != nil {
		s.log.Warn("sign out removed account", "account", accountID, "error", err)
	}
	if err := s.store.DeleteAgentAccount(ctx, accountID); err != nil {
		return err
	}
	s.removeFolder(account.Home)
	s.mu.Lock()
	delete(s.readings, accountID)
	s.mu.Unlock()
	s.notifyDefaultChanged(account.Provider)
	return nil
}

// ConsumeResetCredit spends one Codex usage-limit reset of the account.
func (s *Service) ConsumeResetCredit(ctx context.Context, accountID, idempotencyKey string) (domain.CodexResetCreditOutcome, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return "", err
	}
	crediter, ok := s.providers[account.Provider].(resetCrediter)
	if !ok {
		return "", ports.ErrAgentAccountProviderMismatch
	}
	outcome, err := crediter.ConsumeResetCredit(ctx, account.Home, idempotencyKey)
	if err == nil {
		s.read(ctx, account)
	}
	return outcome, err
}

// BindSession pins a session to accountID for its harness's provider, or to the
// provider's default when accountID is empty. Other harnesses have no accounts.
func (s *Service) BindSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		if accountID != "" {
			return ports.ErrAgentAccountProviderMismatch
		}
		return nil
	}
	if accountID == "" {
		def, err := s.defaultID(ctx, p)
		if err != nil {
			return err
		}
		account, err := s.find(ctx, def)
		if err != nil {
			return err
		}
		// The task would start on the wrong login, or none. Refuse rather than
		// run it there or quietly on another account, which bills someone else.
		// The cached reading decides, so a new task costs no CLI call.
		s.mu.Lock()
		r := s.readings[account.ID]
		s.mu.Unlock()
		if r.knownUnusable() {
			return ports.ErrAgentAccountDefaultUnusable
		}
		if err := s.prepare(s.providers[p], account.Home); err != nil {
			return err
		}
		return s.store.BindSessionAgentAccount(ctx, id, p, def, s.now())
	}
	account, err := s.pick(ctx, harness, accountID)
	if err != nil {
		return err
	}
	return s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now())
}

// CommitSession records that the session now uses accountID, once its agent
// already runs there. The account was validated before the move; probing its
// CLI again here could only turn a live agent into a rollback.
func (s *Service) CommitSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return ports.ErrAgentAccountProviderMismatch
	}
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Provider != p {
		return ports.ErrAgentAccountProviderMismatch
	}
	return s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now())
}

// ValidateAccount checks that accountID can run harness now.
func (s *Service) ValidateAccount(ctx context.Context, harness domain.AgentHarness, accountID string) error {
	_, err := s.pick(ctx, harness, accountID)
	return err
}

// pick resolves an account chosen for harness. It must be of the harness's
// provider and signed in as itself, or the agent would start signed out or as
// someone else.
func (s *Service) pick(ctx context.Context, harness domain.AgentHarness, accountID string) (domain.AgentAccount, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return domain.AgentAccount{}, ports.ErrAgentAccountProviderMismatch
	}
	account, err := s.find(ctx, accountID)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	if s.beingRemoved(account.ID) {
		return domain.AgentAccount{}, ports.ErrAgentAccountUnknown
	}
	if account.Provider != p {
		return domain.AgentAccount{}, ports.ErrAgentAccountProviderMismatch
	}
	s.mu.Lock()
	r := s.readings[account.ID]
	s.mu.Unlock()
	// A failed reading may be transient (a timeout under load); read again
	// rather than refuse the account on it for the rest of the cache window.
	if r == nil || r.identityErr != "" || s.now().Sub(r.at) > freshFor {
		s.read(ctx, account)
		s.mu.Lock()
		r = s.readings[account.ID]
		s.mu.Unlock()
	}
	if r == nil || !r.identity.SignedIn {
		return domain.AgentAccount{}, ports.ErrAgentAccountSignedOut
	}
	if err := s.prepare(s.providers[p], account.Home); err != nil {
		return domain.AgentAccount{}, err
	}
	return account, nil
}

// prepare sets up an account folder for a launch. A folder that cannot link
// to the computer's history is refused: a session there would keep history of
// its own, and a move could not resume it. Other trouble is only logged.
func (s *Service) prepare(provider ports.AgentAccountProviderCLI, home string) error {
	if home == "" {
		return nil
	}
	err := provider.Prepare(home)
	if errors.Is(err, ports.ErrAgentAccountUnshared) {
		return err
	}
	if err != nil {
		s.log.Warn("prepare account folder", "home", home, "error", err)
	}
	return nil
}

// SessionAccountID is the account a session uses for harness's provider, or
// "" when the harness has no accounts.
func (s *Service) SessionAccountID(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (string, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return "", nil
	}
	account, _, err := s.SessionAccount(ctx, id, p)
	return account.ID, err
}

// SessionLimits reads only the session's pinned account. A refresh bypasses the
// display cache; a binding or identity change never falls back to the default.
func (s *Service) SessionLimits(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, refresh bool) (string, domain.AgentAccountLimits, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return "", domain.AgentAccountLimits{}, ports.ErrAgentAccountProviderMismatch
	}
	account, err := s.boundAccount(ctx, id, p)
	if err != nil {
		return "", domain.AgentAccountLimits{}, err
	}
	s.mu.Lock()
	r := s.readings[account.ID]
	s.mu.Unlock()
	if refresh || r == nil || s.now().Sub(r.at) > freshFor {
		r = s.read(ctx, account)
	}
	if err := ctx.Err(); err != nil {
		return account.ID, domain.AgentAccountLimits{}, err
	}
	current, err := s.boundAccount(ctx, id, p)
	if err != nil {
		return account.ID, domain.AgentAccountLimits{}, err
	}
	if current.ID != account.ID || current.Home != account.Home {
		return account.ID, domain.AgentAccountLimits{}, ports.ErrAgentAccountChanged
	}
	if r != nil && r.mismatch {
		return account.ID, domain.AgentAccountLimits{}, ports.ErrAgentAccountChanged
	}
	if r == nil {
		return account.ID, domain.AgentAccountLimits{}, errors.New("account limits are unavailable")
	}
	if r.identityErr != "" {
		return account.ID, domain.AgentAccountLimits{}, fmt.Errorf("read account identity: %s", r.identityErr)
	}
	if !r.identity.SignedIn {
		return account.ID, domain.AgentAccountLimits{}, ports.ErrAgentAccountSignedOut
	}
	if r.limitsErr != "" {
		return account.ID, domain.AgentAccountLimits{}, fmt.Errorf("read account limits: %s", r.limitsErr)
	}
	if r.limits == nil {
		return account.ID, domain.AgentAccountLimits{}, errors.New("account limits are unavailable")
	}
	return account.ID, *r.limits, nil
}

func (s *Service) boundAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, error) {
	accountID, bound, err := s.store.GetSessionAgentAccount(ctx, id, p)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	if !bound || s.beingRemoved(accountID) {
		return domain.AgentAccount{}, ports.ErrAgentAccountChanged
	}
	account, err := s.find(ctx, accountID)
	if errors.Is(err, ports.ErrAgentAccountUnknown) || (err == nil && account.Provider != p) {
		return domain.AgentAccount{}, ports.ErrAgentAccountChanged
	}
	return account, err
}

// SessionAccount is the account a session uses for the provider now: its
// binding, or the provider's default when it has none or its account is gone.
func (s *Service) SessionAccount(ctx context.Context, id domain.SessionID, p domain.AgentAccountProvider) (domain.AgentAccount, bool, error) {
	accountID, bound, err := s.store.GetSessionAgentAccount(ctx, id, p)
	if err != nil {
		return domain.AgentAccount{}, false, err
	}
	if bound {
		if account, err := s.find(ctx, accountID); err == nil {
			return account, true, nil
		}
	}
	def, err := s.defaultID(ctx, p)
	if err != nil {
		return domain.AgentAccount{}, false, err
	}
	account, err := s.find(ctx, def)
	return account, false, err
}

// SessionAccounts is the account a session uses for each provider.
func (s *Service) SessionAccounts(ctx context.Context, id domain.SessionID) (map[domain.AgentAccountProvider]string, error) {
	out := map[domain.AgentAccountProvider]string{}
	for _, p := range domain.AgentAccountProviders {
		if s.providers[p] == nil {
			continue
		}
		account, _, err := s.SessionAccount(ctx, id, p)
		if err != nil {
			return nil, err
		}
		out[p] = account.ID
	}
	return out, nil
}

// SessionLaunchEnv is the environment that runs each provider's CLI under the
// session's account. A session with no account for a provider yet is bound to
// that provider's default here, so a later default change does not move it.
func (s *Service) SessionLaunchEnv(ctx context.Context, id domain.SessionID) (map[string]string, error) {
	return s.launchEnv(ctx, id, domain.AgentAccount{})
}

// SessionLaunchEnvWith is SessionLaunchEnv with accountID in place of the
// session's account for its provider, for starting an agent on an account the
// session moves to. The session's binding is not changed.
func (s *Service) SessionLaunchEnvWith(ctx context.Context, id domain.SessionID, accountID string) (map[string]string, error) {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return nil, err
	}
	if s.beingRemoved(account.ID) {
		return nil, ports.ErrAgentAccountUnknown
	}
	return s.launchEnv(ctx, id, account)
}

func (s *Service) launchEnv(ctx context.Context, id domain.SessionID, override domain.AgentAccount) (map[string]string, error) {
	env := map[string]string{}
	var errs []error
	for _, p := range domain.AgentAccountProviders {
		provider := s.providers[p]
		if provider == nil {
			continue
		}
		account := override
		if override.Provider != p {
			var bound bool
			var err error
			account, bound, err = s.SessionAccount(ctx, id, p)
			if err == nil && bound && s.beingRemoved(account.ID) {
				// Its account is going away: start on the default, and say so.
				var def string
				if def, err = s.defaultID(ctx, p); err == nil {
					account, err = s.find(ctx, def)
					bound = false
				}
			}
			if err != nil {
				errs = append(errs, err)
				continue
			}
			if !bound {
				if err := s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now()); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if account.Device() {
			continue
		}
		// ponytail: a later link failure is only logged here, as this path cannot
		// refuse the launch; binding and moves already refused it.
		if err := s.prepare(provider, account.Home); err != nil {
			errs = append(errs, err)
		}
		for key, value := range provider.LaunchEnv(account.Home) {
			env[key] = value
		}
	}
	return env, errors.Join(errs...)
}

// AccountUsage is one account's model usage in AO over a period.
type AccountUsage struct {
	AccountID    string
	Days         []DayUsage
	Models       []ModelUsage
	InputTokens  int64
	OutputTokens int64
	CostNanos    int64
}

// DayUsage is one UTC day of an account's usage.
type DayUsage struct {
	Day       string
	Tokens    int64
	CostNanos int64
}

// ModelUsage is one model's share of an account's usage.
type ModelUsage struct {
	Model     string
	Tokens    int64
	CostNanos int64
}

// Usage sums each account's usage in AO sessions over the last days, from AO's
// own reading of the session logs.
func (s *Service) Usage(ctx context.Context, days int) ([]AccountUsage, error) {
	if days <= 0 || days > 90 {
		days = 30
	}
	since := s.now().AddDate(0, 0, -days+1).Truncate(24 * time.Hour)
	rows, err := s.store.AgentAccountUsageSince(ctx, since)
	if err != nil {
		return nil, err
	}
	byAccount := map[string]*AccountUsage{}
	dayIndex := map[string]map[string]int{}
	modelIndex := map[string]map[string]int{}
	for _, row := range rows {
		u := byAccount[row.AccountID]
		if u == nil {
			u = &AccountUsage{AccountID: row.AccountID}
			byAccount[row.AccountID] = u
			dayIndex[row.AccountID], modelIndex[row.AccountID] = map[string]int{}, map[string]int{}
		}
		tokens := row.InputTokens + row.OutputTokens
		u.InputTokens += row.InputTokens
		u.OutputTokens += row.OutputTokens
		u.CostNanos += row.CostNanos
		if i, ok := dayIndex[row.AccountID][row.Day]; ok {
			u.Days[i].Tokens += tokens
			u.Days[i].CostNanos += row.CostNanos
		} else {
			dayIndex[row.AccountID][row.Day] = len(u.Days)
			u.Days = append(u.Days, DayUsage{Day: row.Day, Tokens: tokens, CostNanos: row.CostNanos})
		}
		if i, ok := modelIndex[row.AccountID][row.Model]; ok {
			u.Models[i].Tokens += tokens
			u.Models[i].CostNanos += row.CostNanos
		} else {
			modelIndex[row.AccountID][row.Model] = len(u.Models)
			u.Models = append(u.Models, ModelUsage{Model: row.Model, Tokens: tokens, CostNanos: row.CostNanos})
		}
	}
	out := make([]AccountUsage, 0, len(byAccount))
	for _, u := range byAccount {
		sort.Slice(u.Models, func(i, j int) bool { return u.Models[i].Tokens > u.Models[j].Tokens })
		out = append(out, *u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].AccountID < out[j].AccountID })
	return out, nil
}
