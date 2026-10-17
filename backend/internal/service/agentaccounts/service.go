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
	log       *slog.Logger
	now       func() time.Time

	mu       sync.Mutex
	readings map[string]*reading
	logins   map[string]*login
}

// reading is the last identity and limits read for one account.
type reading struct {
	identity    domain.AgentAccountIdentityCheck
	identityErr string
	limits      *domain.AgentAccountLimits
	limitsErr   string
	at          time.Time
}

type login struct {
	id       string
	provider domain.AgentAccountProvider
	home     string
	terminal string
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
		now: func() time.Time { return time.Now().UTC() }, readings: map[string]*reading{}, logins: map[string]*login{},
	}
}

// SetTerminals late-binds the terminal sign-ins run in, which the daemon builds
// after Session Manager.
func (s *Service) SetTerminals(terminals Terminals) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminals = terminals
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
	return refresh || r == nil || s.now().Sub(r.at) > freshFor
}

// read asks the provider's CLI for the account's identity and limits.
func (s *Service) read(ctx context.Context, account domain.AgentAccount) {
	provider := s.providers[account.Provider]
	if provider == nil {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	r := &reading{at: s.now()}
	identity, err := provider.Identity(ctx, account.Home)
	r.identity = identity
	if err != nil {
		r.identityErr = err.Error()
	}
	switch {
	case err != nil:
	case !identity.SignedIn:
		r.limitsErr = "signed out"
	case !account.Device() && account.Email != "" && !strings.EqualFold(identity.Email, account.Email):
		// The right-account check: the folder must still hold the login it was added with.
		r.identity.SignedIn = false
		r.identityErr = fmt.Sprintf("the folder is now signed in as %s, not %s", identity.Email, account.Email)
	default:
		if limits, limitsErr := provider.Limits(ctx, account.Home); limitsErr != nil {
			r.limitsErr = limitsErr.Error()
		} else {
			r.limits = &limits
		}
	}
	s.mu.Lock()
	s.readings[account.ID] = r
	s.mu.Unlock()
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
	case errors.Is(err, ports.ErrAgentAccountUnknown):
		return domain.DeviceAgentAccountID(p), nil // the picked default was removed
	case err != nil:
		return "", err
	}
	return id, nil
}

// SetDefault makes accountID the account new sessions of its provider start on.
// Running sessions and the computer's own CLI are not touched.
func (s *Service) SetDefault(ctx context.Context, accountID string) error {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	return s.store.SetAgentAccountDefault(ctx, account.Provider, account.ID)
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
	provider := s.providers[p]
	s.mu.Lock()
	terminals := s.terminals
	s.mu.Unlock()
	if provider == nil || terminals == nil {
		return LoginStart{}, ports.ErrAgentAccountProviderMismatch
	}
	id := uuid.NewString()
	home := filepath.Join(s.root, string(p), id)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return LoginStart{}, fmt.Errorf("create account folder: %w", err)
	}
	// Linking first means the sign-in never creates a private copy of a shared folder.
	if err := s.prepare(provider, home); err != nil {
		_ = os.RemoveAll(home)
		return LoginStart{}, err
	}
	argv, err := provider.LoginArgv(ctx)
	if err != nil {
		_ = os.RemoveAll(home)
		return LoginStart{}, err
	}
	title := map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "Add Claude account", domain.AgentAccountProviderCodex: "Add Codex account"}[p]
	terminal, err := terminals.OpenCommandTerminal(ctx, shellterm.OpenCommandTerminalInput{Argv: argv, Env: provider.LaunchEnv(home), WorkingDir: home, Title: title})
	if err != nil {
		_ = os.RemoveAll(home)
		return LoginStart{}, fmt.Errorf("open sign-in terminal: %w", err)
	}
	s.mu.Lock()
	s.logins[id] = &login{id: id, provider: p, home: home, terminal: terminal.HandleID}
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
	for _, view := range views {
		if view.Account.Provider == op.provider && view.SignedIn && strings.EqualFold(view.Account.Email, identity.Email) {
			s.endLogin(ctx, op, true)
			return LoginDuplicate, &view, nil
		}
	}
	account := domain.AgentAccount{ID: op.id, Provider: op.provider, Home: op.home, Email: identity.Email, Plan: identity.Plan, CreatedAt: s.now()}
	if err := s.store.CreateAgentAccount(ctx, account); err != nil {
		return "", nil, err
	}
	if err := provider.Prepare(op.home); err != nil {
		s.log.Warn("prepare account folder", "account", op.id, "error", err)
	}
	s.endLogin(ctx, op, false)
	s.read(ctx, account)
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

// CancelLogin stops a sign-in and removes its unsaved folder.
func (s *Service) CancelLogin(ctx context.Context, operationID string) error {
	s.mu.Lock()
	op := s.logins[operationID]
	s.mu.Unlock()
	if op == nil {
		return ports.ErrAgentAccountLoginUnknown
	}
	s.endLogin(ctx, op, true)
	return nil
}

func (s *Service) endLogin(ctx context.Context, op *login, removeFolder bool) {
	s.mu.Lock()
	delete(s.logins, op.id)
	s.mu.Unlock()
	s.mu.Lock()
	terminals := s.terminals
	s.mu.Unlock()
	if op.terminal != "" && terminals != nil {
		_ = terminals.CloseShellTerminal(context.WithoutCancel(ctx), op.terminal)
	}
	if removeFolder {
		// A refused duplicate is signed in; signing out first keeps its token
		// from outliving the folder in the Keychain.
		logoutCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 20*time.Second)
		defer cancel()
		if err := s.providers[op.provider].Logout(logoutCtx, op.home); err != nil {
			s.log.Debug("sign out unsaved account folder", "provider", op.provider, "error", err)
		}
		s.removeFolder(op.home)
	}
}

// removeFolder deletes an account folder AO made. Links inside it are removed,
// never followed, so the computer's own history is untouched.
func (s *Service) removeFolder(home string) {
	rel, err := filepath.Rel(s.root, home)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") {
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
	if account.Provider != p {
		return domain.AgentAccount{}, ports.ErrAgentAccountProviderMismatch
	}
	if s.needsReading(account.ID, false) {
		s.read(ctx, account)
	}
	s.mu.Lock()
	r := s.readings[account.ID]
	s.mu.Unlock()
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
