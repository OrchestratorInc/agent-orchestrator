package agentaccounts

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	codexagent "github.com/aoagents/agent-orchestrator/backend/internal/adapters/agent/codex"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/shellterm"
)

const freshFor = 5 * time.Minute

const retryEmptyAfter = 30 * time.Second

const retryFailedAfter = time.Minute

type Terminals interface {
	OpenCommandTerminal(context.Context, shellterm.OpenCommandTerminalInput) (shellterm.ShellTerminal, error)
	CloseShellTerminal(context.Context, string) error
}

type resetCrediter interface {
	ConsumeResetCredit(ctx context.Context, home, idempotencyKey string) (domain.CodexResetCreditOutcome, error)
}

type Service struct {
	store      ports.AgentAccountStore
	providers  map[domain.AgentAccountProvider]ports.AgentAccountProviderCLI
	root       string
	terminals  Terminals
	stopAgents func(context.Context, string) error
	limitLog   ports.AgentLimitReadingStore
	log        *slog.Logger
	now        func() time.Time

	mu             sync.Mutex
	readings       map[string]*reading
	readSeq        uint64
	logins         map[string]*login
	removing       map[string]bool
	lastOrg        map[string]seenOrg
	defaultChanged func(domain.AgentAccountProvider)
	loginConfirmed func(domain.AgentAccount, time.Time)
}

type reading struct {
	identity        domain.AgentAccountIdentityCheck
	identityErr     string
	identityProblem domain.AgentAccountProblemKind
	mismatch        bool
	limits          *domain.AgentAccountLimits
	limitsErr       string
	limitsProblem   domain.AgentAccountProblemKind
	failures        int
	at              time.Time
	seq             uint64
	confirmed       *domain.AgentAccountIdentityCheck
	confirmedSeq    uint64
}

func (r *reading) failedOnItsOwn() bool {
	transient := func(k domain.AgentAccountProblemKind) bool {
		return k.Transient() && k != domain.AgentAccountProblemRateLimited
	}
	return transient(r.identityProblem) || transient(r.limitsProblem)
}

var httpStatus429 = regexp.MustCompile(`\b(?:status|http|code)\W{0,3}429\b`)

func problemKind(err error) domain.AgentAccountProblemKind {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return domain.AgentAccountProblemSlow
	case errors.Is(err, ports.ErrAgentBinaryNotFound), errors.Is(err, exec.ErrNotFound):
		return domain.AgentAccountProblemCLIMissing
	}
	text := strings.ToLower(err.Error())
	if strings.Contains(text, "deadline exceeded") || strings.Contains(text, "timed out") {
		return domain.AgentAccountProblemSlow
	}
	if strings.Contains(text, "rate limit") || strings.Contains(text, "rate_limit") || strings.Contains(text, "too many requests") || httpStatus429.MatchString(text) {
		return domain.AgentAccountProblemRateLimited
	}
	return domain.AgentAccountProblemUnavailable
}

func (r *reading) knownUnusable() bool {
	return r != nil && (r.mismatch || (r.identityErr == "" && !r.identity.LoggedIn))
}

type login struct {
	id       string
	provider domain.AgentAccountProvider
	home     string
	terminal string
	account  *domain.AgentAccount
}

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
		now: func() time.Time { return time.Now().UTC() }, readings: map[string]*reading{}, logins: map[string]*login{}, removing: map[string]bool{}, lastOrg: map[string]seenOrg{},
	}
}

func (s *Service) SetTerminals(terminals Terminals) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.terminals = terminals
}

func (s *Service) SetAgentStopper(stop func(ctx context.Context, accountID string) error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.stopAgents = stop
}

func (s *Service) SetLimitLog(log ports.AgentLimitReadingStore) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.limitLog = log
}

func (s *Service) KeepReadingLimits(ctx context.Context, every time.Duration) {
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if !s.keepingLimits(ctx) {
				continue
			}
			if _, err := s.List(ctx, false); err != nil && ctx.Err() == nil {
				s.log.DebugContext(ctx, "background limits read failed", "error", err)
			}
		}
	}
}

func (s *Service) keepingLimits(ctx context.Context) bool {
	s.mu.Lock()
	log := s.limitLog
	s.mu.Unlock()
	return log != nil && log.LimitHistoryEnabled(ctx)
}

func (s *Service) recordLimits(ctx context.Context, account domain.AgentAccount, email, plan string, limits domain.AgentAccountLimits) {
	if len(limits.Windows) == 0 || !s.keepingLimits(ctx) {
		return
	}
	s.mu.Lock()
	log := s.limitLog
	s.mu.Unlock()
	at := limits.FetchedAt
	if at.IsZero() {
		at = s.now()
	}
	readings := make([]domain.AgentLimitReading, 0, len(limits.Windows))
	for _, w := range limits.Windows {
		readings = append(readings, domain.AgentLimitReading{
			AccountID: account.ID, Provider: account.Provider, Email: email, Plan: plan, WindowID: w.ID, Label: w.Label,
			DurationMinutes: w.DurationMinutes, UsedPercent: w.UsedPercent, ResetsAt: w.ResetsAt, ReadAt: at,
		})
	}
	if err := log.RecordAgentLimitReadings(context.WithoutCancel(ctx), readings); err != nil {
		s.log.WarnContext(ctx, "failed to record limit readings", "account", account.ID, "error", err)
	}
}

type AccountView struct {
	Account         domain.AgentAccount
	Default         bool
	SignedIn        bool
	LoggedIn        bool
	IdentityErr     string
	IdentityProblem domain.AgentAccountProblemKind
	Limits          *domain.AgentAccountLimits
	LimitsErr       string
	LimitsProblem   domain.AgentAccountProblemKind
	SameLoginAs     string
	OrgID           string
	OrgName         string
}

type seenOrg struct{ id, name string }

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
		lastOrg := s.lastOrg[account.ID]
		s.mu.Unlock()
		view := AccountView{Account: account, Default: def == account.ID, OrgID: lastOrg.id, OrgName: lastOrg.name}
		if r != nil {
			view.SignedIn, view.IdentityErr, view.Limits, view.LimitsErr = r.identity.SignedIn, r.identityErr, r.limits, r.limitsErr
			view.IdentityProblem, view.LimitsProblem = r.identityProblem, r.limitsProblem
			view.LoggedIn = r.identity.LoggedIn && !r.mismatch
			if r.identity.SignedIn && r.identity.OrgID != "" {
				view.OrgID, view.OrgName = r.identity.OrgID, r.identity.OrgName
			}
			if account.Device() {
				view.Account.Email, view.Account.Plan = r.identity.Email, r.identity.Plan
			} else if r.identity.Plan != "" {
				view.Account.Plan = r.identity.Plan
			}
		}
		views = append(views, view)
	}
	markSameLogin(views)
	return views, nil
}

func markSameLogin(views []AccountView) {
	for i := range views {
		device := &views[i]
		if !device.Account.Device() || !device.SignedIn || device.Account.Email == "" {
			continue
		}
		for _, saved := range views {
			if !saved.Account.Device() && saved.Account.Provider == device.Account.Provider && saved.SignedIn && strings.EqualFold(saved.Account.Email, device.Account.Email) && sameOrg(saved.OrgID, device.OrgID) {
				device.SameLoginAs = saved.Account.ID
				break
			}
		}
	}
}

func sameOrg(a, b string) bool {
	return a == "" || b == "" || a == b
}

func orgLabel(name, id string) string {
	if name != "" {
		return name
	}
	return id
}

func (s *Service) needsReading(id string, refresh bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	r := s.readings[id]
	if refresh || r == nil {
		return true
	}
	ttl := freshFor
	switch {
	case r.failedOnItsOwn():
		ttl = min(freshFor, retryFailedAfter<<min(max(r.failures-1, 0), 8))
	case r.limits != nil && len(r.limits.Windows) == 0:
		ttl = retryEmptyAfter
	}
	return s.now().Sub(r.at) > ttl
}

func (s *Service) read(ctx context.Context, account domain.AgentAccount) *reading {
	provider := s.providers[account.Provider]
	if provider == nil {
		return nil
	}
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	s.mu.Lock()
	s.readSeq++
	r := &reading{at: s.now(), seq: s.readSeq}
	s.mu.Unlock()
	identity, err := provider.Identity(ctx, account.Home)
	r.identity = identity
	if err != nil {
		r.identityErr = err.Error()
		r.identityProblem = problemKind(err)
		s.mu.Lock()
		if prev := s.readings[account.ID]; prev != nil && prev.identityErr == "" {
			r.identity, r.limits = prev.identity, prev.limits
		}
		s.mu.Unlock()
	}
	s.mu.Lock()
	known := s.lastOrg[account.ID]
	s.mu.Unlock()
	switch {
	case err != nil:
	case !identity.SignedIn:
		r.limitsErr = "signed out"
	case !account.Device() && account.Email != "" && !strings.EqualFold(identity.Email, account.Email):
		r.identity.SignedIn = false
		r.mismatch = true
		r.identityProblem = domain.AgentAccountProblemOtherLogin
		r.identityErr = fmt.Sprintf("the folder is now signed in as %s, not %s", identity.Email, account.Email)
	case !account.Device() && !sameOrg(known.id, identity.OrgID):
		r.identity.SignedIn = false
		r.mismatch = true
		r.identityProblem = domain.AgentAccountProblemOtherOrg
		r.identityErr = fmt.Sprintf("the folder is now signed in to %s, not %s", orgLabel(identity.OrgName, identity.OrgID), orgLabel(known.name, known.id))
	default:
		if limits, limitsErr := provider.Limits(ctx, account.Home); limitsErr != nil {
			r.limitsErr = limitsErr.Error()
			r.limitsProblem = problemKind(limitsErr)
			s.mu.Lock()
			if prev := s.readings[account.ID]; prev != nil && prev.limits != nil && strings.EqualFold(prev.identity.Email, identity.Email) {
				r.limits = prev.limits
			}
			s.mu.Unlock()
		} else {
			r.limits = &limits
			email, plan := identity.Email, identity.Plan
			if email == "" {
				email = account.Email
			}
			if plan == "" {
				plan = account.Plan
			}
			s.recordLimits(ctx, account, email, plan, limits)
		}
	}
	if err == nil {
		confirmed := r.identity
		r.confirmed, r.confirmedSeq = &confirmed, r.seq
	}
	if caller.Err() != nil {
		return r
	}
	s.mu.Lock()
	if prev := s.readings[account.ID]; prev != nil {
		if prev.confirmedSeq > r.seq {
			s.mu.Unlock()
			return prev
		}
		if err != nil {
			r.confirmed, r.confirmedSeq = prev.confirmed, prev.confirmedSeq
		}
	}
	if r.failedOnItsOwn() {
		r.failures = 1
		if prev := s.readings[account.ID]; prev != nil && prev.failedOnItsOwn() {
			r.failures = prev.failures + 1
		}
	}
	s.readings[account.ID] = r
	if err == nil && r.identity.SignedIn && !r.mismatch && r.identity.OrgID != "" && !account.Device() {
		s.lastOrg[account.ID] = seenOrg{id: r.identity.OrgID, name: r.identity.OrgName}
	}
	confirmed := s.loginConfirmed
	s.mu.Unlock()
	if confirmed != nil && err == nil && r.identity.SignedIn && r.limits != nil && r.limitsErr == "" {
		confirmed(account, r.at)
	}
	return r
}

func (s *Service) ConfirmedLogins() map[string]domain.AgentAccountIdentityCheck {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]domain.AgentAccountIdentityCheck, len(s.readings))
	for id, r := range s.readings {
		if r.confirmed != nil {
			out[id] = *r.confirmed
		}
	}
	return out
}

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

func (s *Service) defaultID(ctx context.Context, p domain.AgentAccountProvider) (string, error) {
	id, ok, err := s.store.GetAgentAccountDefault(ctx, p)
	if err != nil || !ok {
		return domain.DeviceAgentAccountID(p), err
	}
	switch _, err := s.find(ctx, id); {
	case errors.Is(err, ports.ErrAgentAccountUnknown) || s.beingRemoved(id):
		return domain.DeviceAgentAccountID(p), nil
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

func (s *Service) DefaultSignIn(ctx context.Context, harness domain.AgentHarness) (identity domain.AgentAccountIdentityCheck, handled bool) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return identity, false
	}
	def, err := s.defaultID(ctx, p)
	if err != nil || def == domain.DeviceAgentAccountID(p) {
		return identity, false
	}
	account, err := s.find(ctx, def)
	if err != nil {
		return identity, false
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
		return identity, false
	}
	if !r.identity.SignedIn {
		next, err := s.newSessionAccount(ctx, p)
		switch {
		case err == nil && next.Device():
			return identity, false
		case err == nil:
			account = next
			if r = s.cachedReading(next.ID); r == nil {
				return identity, false
			}
		}
	}
	identity = r.identity
	if account.Email != "" {
		identity.Email = account.Email
	}
	return identity, true
}

func (s *Service) DefaultLaunchEnv(ctx context.Context, harness domain.AgentHarness) (map[string]string, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return nil, nil
	}
	account, err := s.newSessionAccount(ctx, p)
	if err != nil {
		return nil, err
	}
	return s.providers[p].LaunchEnv(account.Home), nil
}

func (s *Service) SetDefaultChanged(changed func(domain.AgentAccountProvider)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.defaultChanged = changed
}

func (s *Service) SetLoginConfirmed(confirmed func(domain.AgentAccount, time.Time)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loginConfirmed = confirmed
}

func (s *Service) notifyDefaultChanged(p domain.AgentAccountProvider) {
	s.mu.Lock()
	changed := s.defaultChanged
	s.mu.Unlock()
	if changed != nil {
		changed(p)
	}
}

func (s *Service) RunsOnComputerLogin(ctx context.Context, harness domain.AgentHarness, accountID string) bool {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return true
	}
	if accountID == "" {
		account, err := s.newSessionAccount(ctx, p)
		if err != nil {
			return true
		}
		accountID = account.ID
	}
	return accountID == domain.DeviceAgentAccountID(p)
}

func (s *Service) newSessionAccount(ctx context.Context, p domain.AgentAccountProvider) (domain.AgentAccount, error) {
	def, err := s.defaultID(ctx, p)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	account, err := s.find(ctx, def)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	if p == domain.AgentAccountProviderCodex && codexagent.UsesCustomModelProvider() {
		return account, nil
	}
	r := s.cachedReading(account.ID)
	if !r.knownUnusable() {
		return account, nil
	}
	if s.now().Sub(r.at) > freshFor {
		if r = s.read(ctx, account); !r.knownUnusable() {
			return account, nil
		}
	}
	accounts, err := s.accounts(ctx)
	if err != nil {
		return domain.AgentAccount{}, err
	}
	for _, other := range accounts {
		if other.Provider != p || other.ID == account.ID || s.beingRemoved(other.ID) {
			continue
		}
		r := s.cachedReading(other.ID)
		if r == nil || r.identityErr != "" || s.now().Sub(r.at) > freshFor {
			r = s.read(ctx, other)
		}
		if r != nil && r.identity.SignedIn {
			return other, nil
		}
	}
	return domain.AgentAccount{}, ports.ErrAgentAccountDefaultUnusable
}

func (s *Service) cachedReading(id string) *reading {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readings[id]
}

func (s *Service) RecheckSessionAccount(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return
	}
	account, _, err := s.SessionAccount(ctx, id, p)
	if err != nil {
		s.log.Debug("recheck session account", "session", id, "error", err)
		return
	}
	s.read(ctx, account)
}

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

type LoginStart struct {
	OperationID string
	Provider    domain.AgentAccountProvider
	Terminal    shellterm.ShellTerminal
}

func (s *Service) StartLogin(ctx context.Context, p domain.AgentAccountProvider) (LoginStart, error) {
	return s.startLogin(ctx, p, nil)
}

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
	discard := func() {
		if account == nil {
			_ = os.RemoveAll(home)
		}
	}
	if err := os.MkdirAll(home, 0o700); err != nil {
		return LoginStart{}, fmt.Errorf("create account folder: %w", err)
	}
	if err := s.prepare(provider, home); err != nil {
		discard()
		return LoginStart{}, err
	}
	argv, err := provider.LoginArgv(ctx)
	if err != nil {
		discard()
		return LoginStart{}, err
	}
	terminal, err := terminals.OpenCommandTerminal(ctx, shellterm.OpenCommandTerminalInput{
		Owner: "agentaccounts:" + string(p), Argv: argv, Env: provider.LaunchEnv(home), WorkingDir: home, Title: title,
	})
	if err != nil {
		discard()
		return LoginStart{}, fmt.Errorf("open sign-in terminal: %w", err)
	}
	s.mu.Lock()
	s.logins[id] = &login{id: id, provider: p, home: home, terminal: terminal.HandleID, account: account}
	s.mu.Unlock()
	return LoginStart{OperationID: id, Provider: p, Terminal: terminal}, nil
}

type LoginStatus string

const (
	LoginWaiting         LoginStatus = "waiting"
	LoginComplete        LoginStatus = "complete"
	LoginDuplicate       LoginStatus = "duplicate"
	LoginMismatch        LoginStatus = "mismatch"
	LoginNotSubscription LoginStatus = "not_subscription"
)

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
		if identity.LoggedIn {
			return LoginNotSubscription, nil, nil
		}
		return LoginWaiting, nil, nil
	}
	s.read(ctx, domain.AgentAccount{ID: domain.DeviceAgentAccountID(op.provider), Provider: op.provider})
	views, err := s.List(ctx, false)
	if err != nil {
		return "", nil, err
	}
	if !s.claimLogin(op) {
		return "", nil, ports.ErrAgentAccountLoginUnknown
	}
	for _, view := range views {
		if op.account != nil && (view.Account.ID == op.account.ID || view.Account.Device()) {
			continue
		}
		heldBy := view.SignedIn || (op.account == nil && !view.Account.Device() && view.Account.Email != "")
		if view.Account.Provider == op.provider && heldBy && strings.EqualFold(view.Account.Email, identity.Email) && sameOrg(view.OrgID, identity.OrgID) {
			s.endLogin(ctx, op, true)
			return LoginDuplicate, &view, nil
		}
	}
	if op.account != nil {
		return s.finishSignInAgain(ctx, op, identity, views)
	}
	account := domain.AgentAccount{ID: op.id, Provider: op.provider, Home: op.home, Email: identity.Email, Plan: identity.Plan, CreatedAt: s.now()}
	if err := s.store.CreateAgentAccount(ctx, account); err != nil {
		s.releaseLogin(op)
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

func (s *Service) claimLogin(op *login) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.logins[op.id] != op {
		return false
	}
	delete(s.logins, op.id)
	return true
}

func (s *Service) releaseLogin(op *login) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.logins[op.id] = op
}

func (s *Service) finishSignInAgain(ctx context.Context, op *login, identity domain.AgentAccountIdentityCheck, views []AccountView) (LoginStatus, *AccountView, error) {
	var saved *AccountView
	for i := range views {
		if views[i].Account.ID == op.account.ID {
			saved = &views[i]
		}
	}
	s.mu.Lock()
	known := s.lastOrg[op.account.ID]
	s.mu.Unlock()
	if (op.account.Email != "" && !strings.EqualFold(op.account.Email, identity.Email)) || !sameOrg(known.id, identity.OrgID) {
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

func (s *Service) removeFolder(home string) {
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

func (s *Service) Remove(ctx context.Context, accountID string) error {
	account, err := s.find(ctx, accountID)
	if err != nil {
		return err
	}
	if account.Device() {
		return ports.ErrAgentAccountDeviceFixed
	}
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
	delete(s.lastOrg, accountID)
	s.mu.Unlock()
	s.notifyDefaultChanged(account.Provider)
	return nil
}

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

func (s *Service) BindSession(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		if accountID != "" {
			return ports.ErrAgentAccountProviderMismatch
		}
		return nil
	}
	if accountID == "" {
		account, err := s.newSessionAccount(ctx, p)
		if err != nil {
			return err
		}
		if err := s.prepare(s.providers[p], account.Home); err != nil {
			return err
		}
		return s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now())
	}
	account, err := s.pick(ctx, harness, accountID)
	if err != nil {
		return err
	}
	return s.store.BindSessionAgentAccount(ctx, id, p, account.ID, s.now())
}

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

func (s *Service) ValidateAccount(ctx context.Context, harness domain.AgentHarness, accountID string) error {
	_, err := s.pick(ctx, harness, accountID)
	return err
}

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

func (s *Service) SessionAccountID(ctx context.Context, id domain.SessionID, harness domain.AgentHarness) (string, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return "", nil
	}
	account, _, err := s.SessionAccount(ctx, id, p)
	return account.ID, err
}

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

func (s *Service) HandoffTarget(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, model string) (string, error) {
	p, ok := domain.AgentAccountProviderFor(harness)
	if !ok || s.providers[p] == nil {
		return "", ports.ErrAgentAccountProviderMismatch
	}
	current, err := s.boundAccount(ctx, id, p)
	if err != nil {
		return "", err
	}
	accounts, err := s.accounts(ctx)
	if err != nil {
		return "", err
	}
	headroom := make([]float64, len(accounts))
	var wg sync.WaitGroup
	for i, account := range accounts {
		if account.Provider != p || account.ID == current.ID || s.beingRemoved(account.ID) {
			continue
		}
		wg.Go(func() {
			if r := s.read(ctx, account); r != nil && r.identityErr == "" && r.identity.SignedIn && r.limitsErr == "" && r.limits != nil {
				headroom[i], _ = r.limits.HandoffHeadroom(p, model)
			}
		})
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if after, err := s.boundAccount(ctx, id, p); err != nil {
		return "", err
	} else if after.ID != current.ID {
		return "", ports.ErrAgentAccountChanged
	}
	best := -1
	for i, room := range headroom {
		if room > 0 && (best < 0 || room > headroom[best]) {
			best = i
		}
	}
	if best < 0 {
		s.log.Info("account handoff: no target", "session", id, "model", model)
		return "", nil
	}
	s.log.Info("account handoff: target", "session", id, "model", model, "target", accounts[best].ID)
	return accounts[best].ID, nil
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

func (s *Service) SessionLaunchEnv(ctx context.Context, id domain.SessionID) (map[string]string, error) {
	return s.launchEnv(ctx, id, domain.AgentAccount{})
}

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
		if err := s.prepare(provider, account.Home); err != nil {
			errs = append(errs, err)
		}
		for key, value := range provider.LaunchEnv(account.Home) {
			env[key] = value
		}
	}
	return env, errors.Join(errs...)
}

type AccountUsage struct {
	AccountID    string
	Days         []DayUsage
	Models       []ModelUsage
	InputTokens  int64
	OutputTokens int64
	CostNanos    int64
}

type DayUsage struct {
	Day       string
	Tokens    int64
	CostNanos int64
}

type ModelUsage struct {
	Model     string
	Tokens    int64
	CostNanos int64
}

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
