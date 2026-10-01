// Package accountsmanager exposes a safe, daemon-owned projection of the
// private Accounts Manager runner.
package accountsmanager

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	core "github.com/aoagents/agent-orchestrator/backend/internal/accountsmanager"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// ErrRoutingNotConfigured and the other routing errors describe selection failures without secrets.
var (
	ErrRoutingNotConfigured      = errors.New("accounts manager routing is not configured")
	ErrRoutingAccountUnavailable = errors.New("accounts manager pinned account is unavailable")
	ErrRoutingNoEligibleAccount  = errors.New("accounts manager has no eligible account")
)

// Availability describes freshness of the runner-backed account projection.
type Availability string

// AvailabilityStarting and the other values describe projection availability.
const (
	AvailabilityStarting Availability = "starting"
	AvailabilityReady    Availability = "ready"
	AvailabilityDegraded Availability = "degraded"
)

// Account uses an opaque public ID and excludes raw credential material.
type Account struct {
	Verification       string
	VerifiedAt         time.Time
	Label              string
	Generation         uint64
	ReconnectSupported bool
	ID                 string
	Provider           core.Provider
	Kind               core.CredentialKind
	Email              string
	Status             core.CredentialState
	Disabled           bool
	Unavailable        bool
	CreatedAt          time.Time
	UpdatedAt          time.Time
	LastRefreshedAt    time.Time
	QuotaSupported     bool
	Cooldowns          []core.CredentialCooldown
}

// OAuthSession exposes login progress without the runner's private callback state.
type OAuthSession struct {
	AccountID        string
	ID               string
	Provider         core.Provider
	Mode             core.OAuthMode
	Status           core.OAuthState
	FailureCode      string
	AuthorizationURL string
	UserCode         string
	ExpiresAt        time.Time
	terminalAt       time.Time
}

// Snapshot is a revisioned, redacted projection suitable for public events.
type Snapshot struct {
	Revision      int64
	Availability  Availability
	Stale         bool
	Accounts      []Account
	OAuthSessions []OAuthSession
	Routing       []domain.AccountsManagerRoutingPolicy
}

// Client limits projection access to credential summaries and validated login events.
type Client interface {
	ListCredentials(context.Context) ([]core.CredentialSummary, error)
	CredentialPublicID(string) (string, error)
	OAuthPublicID(string) (string, error)
	StreamOAuthEvents(context.Context, func(core.OAuthEvent) error) error
}

type lifecycleClient interface {
	Client
	StartOAuth(context.Context, core.Provider, core.OAuthMode) (core.OAuthSession, error)
	CancelOAuth(context.Context, string) error
	AddAPIKey(context.Context, core.APIKeyInput) (core.CredentialSummary, error)
	ImportCredential(context.Context, core.CredentialImport) (core.CredentialSummary, error)
	SetCredentialDisabled(context.Context, string, bool) error
	RefreshCredential(context.Context, string) (core.CredentialSummary, error)
	RemoveCredential(context.Context, string) error
	ListCredentialModels(context.Context, string) ([]core.CredentialModel, error)
	FetchCredentialQuota(context.Context, string) (core.CredentialQuota, error)
	ResetCredentialQuota(context.Context, string) error
}

type routingClient interface {
	MintRoute(context.Context, core.Provider, string, string, string, int64) (core.RouteCapability, error)
	SynchronizeBindings(context.Context, core.BindingSnapshot) error
}

// RoutingStore persists public account choices without runner credentials.
type RoutingStore interface {
	GetAccountsManagerRoutingPolicy(context.Context, domain.AccountsManagerProvider) (domain.AccountsManagerRoutingPolicy, error)
	PutAccountsManagerRoutingPolicy(context.Context, domain.AccountsManagerRoutingPolicy) error
	GetAccountsManagerSessionRoute(context.Context, domain.SessionID, domain.AccountsManagerProvider) (domain.AccountsManagerSessionRoute, bool, error)
	GetOrCreateAccountsManagerSessionRoute(context.Context, domain.AccountsManagerSessionRoute) (domain.AccountsManagerSessionRoute, bool, error)
}

// LaunchRoute is private child-process material and must not be persisted or exposed.
type LaunchRoute struct {
	Provider        core.Provider
	AccountID       string
	BaseURL         string
	Token           string
	BindingRevision int64
}

// Service owns public projections and keeps private runner references out of them.
type Service struct {
	client       Client
	routingStore RoutingStore
	choiceMu     sync.Mutex
	bindingsMu   sync.Mutex

	mu          sync.RWMutex
	snapshot    Snapshot
	rawAccounts map[string]string
	rawOAuth    map[string]string
	subscribers map[chan Snapshot]struct{}
}

// PrepareAgentLaunchRoute adapts a prepared pin to the child-launch boundary.
func (s *Service) PrepareAgentLaunchRoute(ctx context.Context, sessionID domain.SessionID, provider domain.AccountsManagerProvider, model string) (*ports.AccountsManagerLaunchRoute, error) {
	route, err := s.PrepareLaunchRoute(ctx, sessionID, core.Provider(provider), model)
	if err != nil || route == nil {
		return nil, err
	}
	return &ports.AccountsManagerLaunchRoute{BaseURL: route.BaseURL, Token: route.Token, BindingRevision: route.BindingRevision}, nil
}

// HasAgentSessionRoute checks durable intent without minting a token or selecting an account.
func (s *Service) HasAgentSessionRoute(ctx context.Context, sessionID domain.SessionID, provider domain.AccountsManagerProvider) (bool, error) {
	if s.routingStore == nil {
		return false, core.ErrUnavailable
	}
	route, found, err := s.routingStore.GetAccountsManagerSessionRoute(ctx, sessionID, provider)
	return found && route.Mode != domain.AccountsManagerNative, err
}

// AgentRoutingEnabled reads opt-in policy without changing any session's binding.
func (s *Service) AgentRoutingEnabled(ctx context.Context, provider domain.AccountsManagerProvider) (bool, error) {
	if !provider.Valid() || s.routingStore == nil {
		return false, nil
	}
	policy, err := s.routingStore.GetAccountsManagerRoutingPolicy(ctx, provider)
	if err != nil {
		return false, fmt.Errorf("read accounts manager routing policy: %w", err)
	}
	return policy.Enabled, nil
}

// New accepts an optional routing store without starting background work.
func New(client Client, stores ...RoutingStore) *Service {
	var routingStore RoutingStore
	if len(stores) > 0 {
		routingStore = stores[0]
	}
	return &Service{
		client:       client,
		routingStore: routingStore,
		snapshot:     Snapshot{Revision: time.Now().UnixMilli(), Availability: AvailabilityStarting, Stale: true},
		rawAccounts:  make(map[string]string), rawOAuth: make(map[string]string), subscribers: make(map[chan Snapshot]struct{}),
	}
}

// Start watches login progress until ctx is cancelled.
func (s *Service) Start(ctx context.Context) {
	if s == nil || s.client == nil {
		return
	}
	go s.watchOAuth(ctx)
	if _, ok := s.routingStore.(bindingStore); ok {
		go s.watchBindings(ctx)
	}
}

func (s *Service) watchOAuth(ctx context.Context) {
	delay := time.Second
	for ctx.Err() == nil {
		err := s.client.StreamOAuthEvents(ctx, func(event core.OAuthEvent) error { s.applyOAuthEvent(ctx, event); return nil })
		if ctx.Err() != nil {
			return
		}
		s.markDegraded()
		if err == nil {
			delay = time.Second
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		if delay < 30*time.Second {
			delay *= 2
			if delay > 30*time.Second {
				delay = 30 * time.Second
			}
		}
	}
}

// Snapshot returns a detached copy so callers cannot mutate shared state.
func (s *Service) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneTerminalLocked(time.Now())
	return cloneSnapshot(s.snapshot)
}

// Refresh retains a stale snapshot when the runner cannot supply fresh observations.
func (s *Service) Refresh(ctx context.Context) (Snapshot, error) {
	credentials, err := s.client.ListCredentials(ctx)
	if err != nil {
		s.markDegraded()
		return s.Snapshot(), err
	}
	accounts := make([]Account, 0, len(credentials))
	raw := make(map[string]string, len(credentials))
	for _, credential := range credentials {
		id, idErr := s.client.CredentialPublicID(credential.Ref)
		if idErr != nil {
			s.markDegraded()
			return s.Snapshot(), idErr
		}
		raw[id] = credential.Ref
		accounts = append(accounts, accountFromCredential(id, credential))
	}
	routing, err := s.loadRouting(ctx)
	if err != nil {
		s.markDegraded()
		return s.Snapshot(), err
	}
	s.mu.Lock()
	s.rawAccounts = raw
	s.snapshot.Accounts = accounts
	s.snapshot.Routing = routing
	s.snapshot.Availability = AvailabilityReady
	s.snapshot.Stale = false
	s.bumpLocked()
	result := cloneSnapshot(s.snapshot)
	s.mu.Unlock()
	return result, nil
}

// SetRoutingPolicy accepts one explicit default without changing existing session bindings.
func (s *Service) SetRoutingPolicy(ctx context.Context, provider core.Provider, enabled bool, accountIDs []string) (Snapshot, error) {
	s.choiceMu.Lock()
	defer s.choiceMu.Unlock()
	domainProvider, ok := domainProvider(provider)
	if !ok || s.routingStore == nil {
		return s.Snapshot(), core.ErrUnsupportedProvider
	}
	snapshot, err := s.Refresh(ctx)
	if err != nil {
		return snapshot, err
	}
	seen := make(map[string]struct{}, len(accountIDs))
	eligible := false
	for _, id := range accountIDs {
		if id == "" {
			return s.Snapshot(), core.ErrInvalidCredential
		}
		if _, duplicate := seen[id]; duplicate {
			return s.Snapshot(), core.ErrCredentialConflict
		}
		seen[id] = struct{}{}
		account, found := accountByID(snapshot.Accounts, id)
		if !found || account.Provider != provider {
			return s.Snapshot(), core.ErrCredentialNotFound
		}
		eligible = eligible || accountUsable(account, "", time.Now())
	}
	if len(accountIDs) > 1 || (enabled && (!eligible || len(accountIDs) != 1)) {
		return s.Snapshot(), ErrRoutingNotConfigured
	}
	policy := domain.AccountsManagerRoutingPolicy{Provider: domainProvider, Enabled: enabled, AccountIDs: append([]string(nil), accountIDs...)}
	if err = s.routingStore.PutAccountsManagerRoutingPolicy(ctx, policy); err != nil {
		return s.Snapshot(), fmt.Errorf("save accounts manager routing policy: %w", err)
	}
	return s.Refresh(ctx)
}

// PrepareLaunchRoute preserves an existing pin and fails if that account becomes unusable.
func (s *Service) PrepareLaunchRoute(ctx context.Context, sessionID domain.SessionID, provider core.Provider, model string) (*LaunchRoute, error) {
	domainProvider, ok := domainProvider(provider)
	client, clientOK := s.client.(routingClient)
	if !ok || !clientOK || s.routingStore == nil {
		if !ok {
			return nil, core.ErrUnsupportedProvider
		}
		return nil, core.ErrUnavailable
	}
	if existing, found, err := s.routingStore.GetAccountsManagerSessionRoute(ctx, sessionID, domainProvider); err != nil {
		return nil, fmt.Errorf("read accounts manager session route: %w", err)
	} else if found {
		if existing.Mode == domain.AccountsManagerNative {
			return nil, nil
		}
		return s.preparePinnedRoute(ctx, client, existing, provider, model)
	}
	policy, err := s.routingStore.GetAccountsManagerRoutingPolicy(ctx, domainProvider)
	if err != nil {
		return nil, fmt.Errorf("read accounts manager routing policy: %w", err)
	}
	if !policy.Enabled {
		binding, _, err := s.routingStore.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{SessionID: sessionID, Provider: domainProvider, Mode: domain.AccountsManagerNative, CreatedAt: time.Now().UTC()})
		if err != nil {
			return nil, fmt.Errorf("record native session binding: %w", err)
		}
		if binding.Mode != domain.AccountsManagerNative {
			return s.preparePinnedRoute(ctx, client, binding, provider, model)
		}
		return nil, nil
	}
	if len(policy.AccountIDs) != 1 {
		return nil, ErrRoutingNotConfigured
	}
	snapshot, err := s.Refresh(ctx)
	if err != nil || snapshot.Availability != AvailabilityReady {
		return nil, core.ErrUnavailable
	}
	accountID := policy.AccountIDs[0]
	account, found := accountByID(snapshot.Accounts, accountID)
	if !found || account.Provider != provider || !accountUsable(account, model, time.Now()) {
		return nil, ErrRoutingAccountUnavailable
	}
	ref := s.rawAccountRef(accountID)
	if ref == "" {
		return nil, ErrRoutingAccountUnavailable
	}
	pinned, _, err := s.routingStore.GetOrCreateAccountsManagerSessionRoute(ctx, domain.AccountsManagerSessionRoute{
		SessionID: sessionID, Provider: domainProvider, AccountID: accountID, CreatedAt: time.Now().UTC(),
	})
	if err != nil {
		return nil, fmt.Errorf("pin accounts manager session route: %w", err)
	}
	if pinned.AccountID != accountID {
		if pinned.Mode == domain.AccountsManagerNative {
			return nil, nil
		}
		return s.preparePinnedRoute(ctx, client, pinned, provider, model)
	}
	capability, err := s.mintBoundRoute(ctx, client, pinned, ref)
	if errors.Is(err, core.ErrCredentialNotFound) {
		return nil, ErrRoutingAccountUnavailable
	}
	if err != nil {
		return nil, err
	}
	return &LaunchRoute{Provider: provider, AccountID: accountID, BaseURL: capability.BaseURL, Token: capability.Token, BindingRevision: pinned.Revision}, nil
}

func (s *Service) preparePinnedRoute(ctx context.Context, client routingClient, pinned domain.AccountsManagerSessionRoute, provider core.Provider, model string) (*LaunchRoute, error) {
	if pinned.Blocked {
		return nil, ErrRoutingAccountUnavailable
	}
	if pinned.Mode == domain.AccountsManagerNative {
		return nil, nil
	}
	snapshot, err := s.Refresh(ctx)
	if err != nil || snapshot.Availability != AvailabilityReady {
		return nil, core.ErrUnavailable
	}
	account, found := accountByID(snapshot.Accounts, pinned.AccountID)
	if !found || account.Provider != provider || !accountUsable(account, model, time.Now()) {
		return nil, ErrRoutingAccountUnavailable
	}
	ref := s.rawAccountRef(pinned.AccountID)
	if ref == "" {
		return nil, ErrRoutingAccountUnavailable
	}
	capability, err := s.mintBoundRoute(ctx, client, pinned, ref)
	if err != nil {
		if errors.Is(err, core.ErrCredentialNotFound) {
			return nil, ErrRoutingAccountUnavailable
		}
		return nil, err
	}
	return &LaunchRoute{Provider: provider, AccountID: pinned.AccountID, BaseURL: capability.BaseURL, Token: capability.Token, BindingRevision: pinned.Revision}, nil
}

// Subscribe sends the current snapshot first and closes the subscription on cancellation.
func (s *Service) Subscribe(ctx context.Context) <-chan Snapshot {
	updates := make(chan Snapshot, 8)
	s.mu.Lock()
	s.subscribers[updates] = struct{}{}
	updates <- cloneSnapshot(s.snapshot)
	s.mu.Unlock()
	go func() {
		<-ctx.Done()
		s.mu.Lock()
		if _, ok := s.subscribers[updates]; ok {
			delete(s.subscribers, updates)
			close(updates)
		}
		s.mu.Unlock()
	}()
	return updates
}

// StartOAuth publishes sign-in instructions under an opaque operation ID.
func (s *Service) StartOAuth(ctx context.Context, provider core.Provider, mode core.OAuthMode) (OAuthSession, error) {
	client, ok := s.client.(lifecycleClient)
	if !ok {
		return OAuthSession{}, core.ErrUnavailable
	}
	session, err := client.StartOAuth(ctx, provider, mode)
	if err != nil {
		return OAuthSession{}, err
	}
	return s.publishOAuth(session)
}

// ReconnectOAuth never changes user defaults or session bindings.
func (s *Service) ReconnectOAuth(ctx context.Context, provider core.Provider, mode core.OAuthMode, accountID string, generation uint64) (OAuthSession, error) {
	if err := s.admitAccountMutation(ctx, accountID); err != nil {
		return OAuthSession{}, err
	}
	client, ok := s.client.(interface {
		ReconnectOAuth(context.Context, core.Provider, core.OAuthMode, string, uint64) (core.OAuthSession, error)
	})
	if !ok {
		return OAuthSession{}, core.ErrUnavailable
	}
	snapshot, err := s.Refresh(ctx)
	if err != nil {
		return OAuthSession{}, err
	}
	account, found := accountByID(snapshot.Accounts, accountID)
	if !found || account.Provider != provider {
		return OAuthSession{}, core.ErrCredentialNotFound
	}
	if generation == 0 || generation != account.Generation {
		return OAuthSession{}, core.ErrCredentialConflict
	}
	if !account.ReconnectSupported {
		return OAuthSession{}, core.ErrOperationUnsupported
	}
	ref := s.rawAccountRef(accountID)
	if ref == "" {
		return OAuthSession{}, core.ErrCredentialNotFound
	}
	session, err := client.ReconnectOAuth(ctx, provider, mode, ref, generation)
	if err != nil {
		return OAuthSession{}, err
	}
	return s.publishOAuth(session)
}

func (s *Service) publishOAuth(session core.OAuthSession) (OAuthSession, error) {
	id, err := s.client.OAuthPublicID(session.State)
	if err != nil {
		return OAuthSession{}, err
	}
	public := OAuthSession{ID: id, Provider: session.Provider, Mode: session.Mode, Status: core.OAuthPending, AuthorizationURL: session.AuthorizationURL, UserCode: session.UserCode, ExpiresAt: session.ExpiresAt}
	if session.TargetRef != "" {
		public.AccountID, err = s.client.CredentialPublicID(session.TargetRef)
		if err != nil {
			return OAuthSession{}, err
		}
	}
	s.mu.Lock()
	s.rawOAuth[id] = session.State
	s.upsertOAuthLocked(public)
	s.bumpLocked()
	s.mu.Unlock()
	return public, nil
}

// CancelOAuth treats unknown public operation IDs as already cancelled.
func (s *Service) CancelOAuth(ctx context.Context, id string) error {
	client, ok := s.client.(lifecycleClient)
	if !ok {
		return core.ErrUnavailable
	}
	s.mu.RLock()
	state, found := s.rawOAuth[id]
	s.mu.RUnlock()
	if !found {
		return nil
	}
	return client.CancelOAuth(ctx, state)
}

// AddAPIKey refreshes the safe snapshot after the runner accepts the secret input.
func (s *Service) AddAPIKey(ctx context.Context, input core.APIKeyInput) (Snapshot, error) {
	client, ok := s.client.(lifecycleClient)
	if !ok {
		return s.Snapshot(), core.ErrUnavailable
	}
	if _, err := client.AddAPIKey(ctx, input); err != nil {
		return s.Snapshot(), err
	}
	return s.Refresh(ctx)
}

// ImportCredential refreshes the safe snapshot after a validated runner import.
func (s *Service) ImportCredential(ctx context.Context, input core.CredentialImport) (Snapshot, error) {
	client, ok := s.client.(lifecycleClient)
	if !ok {
		return s.Snapshot(), core.ErrUnavailable
	}
	if _, err := client.ImportCredential(ctx, input); err != nil {
		return s.Snapshot(), err
	}
	return s.Refresh(ctx)
}

// RenameAccount leaves provider identity and user routing choices unchanged.
func (s *Service) RenameAccount(ctx context.Context, id, label string, generation uint64) (Snapshot, error) {
	if err := s.admitAccountMutation(ctx, id); err != nil {
		return s.Snapshot(), err
	}
	_, ref, err := s.resolve(ctx, id)
	if err != nil {
		return s.Snapshot(), err
	}
	client, ok := s.client.(interface {
		RenameCredential(context.Context, string, string, uint64) error
	})
	if !ok {
		return s.Snapshot(), core.ErrUnavailable
	}
	if err := client.RenameCredential(ctx, ref, label, generation); err != nil {
		return s.Snapshot(), err
	}
	return s.Refresh(ctx)
}

// SetDisabled resolves a public account ID before changing runner state.
func (s *Service) SetDisabled(ctx context.Context, id string, disabled bool) (Snapshot, error) {
	if err := s.admitAccountMutation(ctx, id); err != nil {
		return s.Snapshot(), err
	}
	client, ref, err := s.resolve(ctx, id)
	if err != nil {
		return s.Snapshot(), err
	}
	if err := client.SetCredentialDisabled(ctx, ref, disabled); err != nil {
		return s.Snapshot(), err
	}
	return s.Refresh(ctx)
}

// RefreshAccount uses the private credential reference only after resolving the public ID.
func (s *Service) RefreshAccount(ctx context.Context, id string) (Snapshot, error) {
	if err := s.admitAccountMutation(ctx, id); err != nil {
		return s.Snapshot(), err
	}
	client, ref, err := s.resolve(ctx, id)
	if err != nil {
		return s.Snapshot(), err
	}
	if _, err = client.RefreshCredential(ctx, ref); err != nil {
		return s.Snapshot(), err
	}
	return s.Refresh(ctx)
}

// RemoveAccount treats a missing account as an already-completed removal.
func (s *Service) RemoveAccount(ctx context.Context, id string) (Snapshot, error) {
	if store, ok := s.routingStore.(ports.AccountsManagerRemovalStore); ok {
		op, _, err := s.PrepareAccountRemoval(ctx, "remove:"+id, id, 0, false)
		if err != nil {
			return s.Snapshot(), err
		}
		if !op.Phase.Terminal() {
			if len(op.Impact.Sessions) != 0 {
				return s.Snapshot(), domain.ErrAccountsManagerAccountInUse
			}
			if err := store.BeginAccountsManagerRemovalStop(ctx, op.ID); err != nil {
				return s.Snapshot(), err
			}
			if err := s.synchronizeBindings(ctx); err != nil {
				return s.Snapshot(), err
			}
			if err := store.RecordAccountsManagerRemovalBindingsRevoked(ctx, op.ID); err != nil {
				return s.Snapshot(), err
			}
		}
		if err := s.FinalizeAccountRemoval(ctx, op.ID); err != nil {
			return s.Snapshot(), err
		}
		return s.Refresh(ctx)
	}
	s.choiceMu.Lock()
	defer s.choiceMu.Unlock()
	client, ref, err := s.resolve(ctx, id)
	if err != nil && !errors.Is(err, core.ErrCredentialNotFound) {
		return s.Snapshot(), err
	}
	if err == nil {
		if err := client.RemoveCredential(ctx, ref); err != nil {
			return s.Snapshot(), err
		}
	}
	if s.routingStore != nil {
		for _, provider := range []domain.AccountsManagerProvider{domain.AccountsManagerProviderCodex, domain.AccountsManagerProviderClaude} {
			policy, err := s.routingStore.GetAccountsManagerRoutingPolicy(ctx, provider)
			if err != nil {
				return s.Snapshot(), fmt.Errorf("read default after removal: %w", err)
			}
			if len(policy.AccountIDs) != 1 || policy.AccountIDs[0] != id {
				continue
			}
			policy.Enabled, policy.AccountIDs = false, nil
			if err := s.routingStore.PutAccountsManagerRoutingPolicy(ctx, policy); err != nil {
				return s.Snapshot(), fmt.Errorf("clear removed default: %w", err)
			}
		}
	}
	return s.Refresh(ctx)
}

// Models scopes discovery to a resolved public account ID.
func (s *Service) Models(ctx context.Context, id string) ([]core.CredentialModel, error) {
	client, ref, err := s.resolve(ctx, id)
	if err != nil {
		return nil, err
	}
	return client.ListCredentialModels(ctx, ref)
}

// Quota returns only quota data for the resolved account.
func (s *Service) Quota(ctx context.Context, id string) (core.CredentialQuota, error) {
	client, ref, err := s.resolve(ctx, id)
	if err != nil {
		return core.CredentialQuota{}, err
	}
	return client.FetchCredentialQuota(ctx, ref)
}

// ResetQuota requires the runner to advertise reset support for the resolved account.
func (s *Service) ResetQuota(ctx context.Context, id string) error {
	client, ref, err := s.resolve(ctx, id)
	if err != nil {
		return err
	}
	return client.ResetCredentialQuota(ctx, ref)
}

func (s *Service) resolve(ctx context.Context, id string) (lifecycleClient, string, error) {
	client, ok := s.client.(lifecycleClient)
	if !ok {
		return nil, "", core.ErrUnavailable
	}
	if _, err := s.Refresh(ctx); err != nil {
		return nil, "", err
	}
	s.mu.RLock()
	ref, found := s.rawAccounts[id]
	s.mu.RUnlock()
	if !found {
		return nil, "", core.ErrCredentialNotFound
	}
	return client, ref, nil
}

func (s *Service) applyOAuthEvent(ctx context.Context, event core.OAuthEvent) {
	id, err := s.client.OAuthPublicID(event.State)
	if err != nil {
		s.markDegraded()
		return
	}
	public := OAuthSession{ID: id, Provider: event.Provider, Mode: event.Mode, Status: event.Status, FailureCode: event.FailureCode, AuthorizationURL: event.AuthorizationURL, UserCode: event.UserCode, ExpiresAt: event.ExpiresAt}
	if event.TargetRef != "" {
		public.AccountID, err = s.client.CredentialPublicID(event.TargetRef)
		if err != nil {
			s.markDegraded()
			return
		}
	}
	if event.Status != core.OAuthPending {
		public.terminalAt = time.Now()
	}
	s.mu.Lock()
	s.rawOAuth[id] = event.State
	s.upsertOAuthLocked(public)
	s.bumpLocked()
	s.mu.Unlock()
	if event.Status != core.OAuthPending {
		go func() {
			timer := time.NewTimer(time.Minute)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
			s.mu.Lock()
			before := len(s.snapshot.OAuthSessions)
			s.pruneTerminalLocked(time.Now())
			if len(s.snapshot.OAuthSessions) != before {
				s.bumpLocked()
			}
			s.mu.Unlock()
		}()
	}
	if event.Status == core.OAuthCompleted {
		_, _ = s.Refresh(ctx)
	}
}

func (s *Service) upsertOAuthLocked(session OAuthSession) {
	for index := range s.snapshot.OAuthSessions {
		if s.snapshot.OAuthSessions[index].ID != session.ID {
			continue
		}
		if s.snapshot.OAuthSessions[index].Status != core.OAuthPending {
			return
		}
		if session.AuthorizationURL == "" {
			session.AuthorizationURL = s.snapshot.OAuthSessions[index].AuthorizationURL
		}
		if session.UserCode == "" {
			session.UserCode = s.snapshot.OAuthSessions[index].UserCode
		}
		s.snapshot.OAuthSessions[index] = session
		return
	}
	s.snapshot.OAuthSessions = append(s.snapshot.OAuthSessions, session)
}

func (s *Service) markDegraded() {
	s.mu.Lock()
	s.snapshot.Availability = AvailabilityDegraded
	s.snapshot.Stale = true
	s.bumpLocked()
	s.mu.Unlock()
}
func (s *Service) bumpLocked() {
	s.snapshot.Revision++
	snapshot := cloneSnapshot(s.snapshot)
	for subscriber := range s.subscribers {
		select {
		case subscriber <- snapshot:
		default:
			select {
			case <-subscriber:
			default:
			}
			subscriber <- snapshot
		}
	}
}
func (s *Service) pruneTerminalLocked(now time.Time) {
	kept := s.snapshot.OAuthSessions[:0]
	for _, session := range s.snapshot.OAuthSessions {
		if !session.terminalAt.IsZero() && now.Sub(session.terminalAt) >= time.Minute {
			delete(s.rawOAuth, session.ID)
			continue
		}
		kept = append(kept, session)
	}
	s.snapshot.OAuthSessions = kept
}
func accountFromCredential(id string, value core.CredentialSummary) Account {
	return Account{ID: id, Label: value.Label, Generation: value.Generation, ReconnectSupported: value.ReconnectSupported, Verification: value.Verification, VerifiedAt: value.VerifiedAt, Provider: value.Provider, Kind: value.Kind, Email: value.Email, Status: value.Status, Disabled: value.Disabled, Unavailable: value.Unavailable, CreatedAt: value.CreatedAt, UpdatedAt: value.UpdatedAt, LastRefreshedAt: value.LastRefreshedAt, QuotaSupported: value.QuotaSupported, Cooldowns: append([]core.CredentialCooldown(nil), value.Cooldowns...)}
}

func domainProvider(provider core.Provider) (domain.AccountsManagerProvider, bool) {
	switch provider {
	case core.ProviderCodex:
		return domain.AccountsManagerProviderCodex, true
	case core.ProviderClaude:
		return domain.AccountsManagerProviderClaude, true
	default:
		return "", false
	}
}

func accountByID(accounts []Account, id string) (Account, bool) {
	for _, account := range accounts {
		if account.ID == id {
			return account, true
		}
	}
	return Account{}, false
}

func accountUsable(account Account, model string, now time.Time) bool {
	if account.Disabled || account.Unavailable || account.Status != core.CredentialActive {
		return false
	}
	for _, cooldown := range account.Cooldowns {
		active := cooldown.RemainingSeconds > 0
		if !cooldown.RetryAt.IsZero() {
			active = cooldown.RetryAt.After(now)
		}
		if !active {
			continue
		}
		if cooldown.Model == "" || (model != "" && cooldown.Model == model) {
			return false
		}
	}
	return true
}

func (s *Service) rawAccountRef(id string) string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rawAccounts[id]
}

func (s *Service) loadRouting(ctx context.Context) ([]domain.AccountsManagerRoutingPolicy, error) {
	providers := []domain.AccountsManagerProvider{
		domain.AccountsManagerProviderCodex,
		domain.AccountsManagerProviderClaude,
	}
	policies := make([]domain.AccountsManagerRoutingPolicy, 0, len(providers))
	for _, provider := range providers {
		policy := domain.AccountsManagerRoutingPolicy{Provider: provider, AccountIDs: []string{}}
		if s.routingStore != nil {
			stored, err := s.routingStore.GetAccountsManagerRoutingPolicy(ctx, provider)
			if err != nil {
				return nil, fmt.Errorf("read accounts manager routing policy: %w", err)
			}
			policy = stored
			policy.Provider = provider
			if policy.AccountIDs == nil {
				policy.AccountIDs = []string{}
			}
		}

		policies = append(policies, policy)
	}
	return policies, nil
}

func cloneSnapshot(value Snapshot) Snapshot {
	value.Accounts = append([]Account(nil), value.Accounts...)
	value.OAuthSessions = append([]OAuthSession(nil), value.OAuthSessions...)
	value.Routing = append([]domain.AccountsManagerRoutingPolicy(nil), value.Routing...)
	for i := range value.Accounts {
		value.Accounts[i].Cooldowns = append([]core.CredentialCooldown(nil), value.Accounts[i].Cooldowns...)
	}
	for i := range value.Routing {
		value.Routing[i].AccountIDs = append([]string(nil), value.Routing[i].AccountIDs...)
	}
	return value
}
