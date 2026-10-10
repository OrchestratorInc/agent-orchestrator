// Package provideraccounts owns managed account defaults and session assignments.
package provideraccounts

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"maps"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// Service owns managed provider defaults, session assignments, and mutation recovery.
type Service struct {
	store             ports.ProviderAccountStore
	proxy             ports.ProviderAccountProxy
	guard             ports.ProviderAccountSessionGuard
	gate              chan struct{}
	ticketKey         []byte
	endpoint          string
	newID             func() string
	usageMu           sync.Mutex
	usageCache        map[string]cachedUsage
	nativeCheckedAt   time.Time
	signInMu          sync.Mutex
	signInFailures    map[string]string
	signInCheckedAt   time.Time
	leftoverCheckedAt time.Time
	native            ports.ProviderNativeAccountSource
	onReadinessChange func(string)
}

type cachedUsage struct {
	value     domain.ProviderAccountUsage
	expiresAt time.Time
}

const providerUsageCacheTTL = 2 * time.Minute

// A failed lookup is retried soon instead of hiding usage for a full window.
const providerUsageFailureTTL = 15 * time.Second

// Native logins are re-read at most this often unless a caller forces it.
const providerNativeCheckInterval = 5 * time.Minute

// CLIProxy's verdict on account sign-ins is re-read at most this often.
const providerSignInCheckInterval = 30 * time.Second

// Usage is supplemental account information. It must never hold the account
// catalogue open for the full lifetime of an upstream request.
const providerUsageLookupTimeout = 5 * time.Second

// Bound account work independently of the HTTP client's lifetime. A timed-out
// admitted change stays in the existing journal for the daemon to recover.
const providerAccountOperationTimeout = 15 * time.Second

// New creates the account service with a stable private ticket identity.
func New(store ports.ProviderAccountStore, proxy ports.ProviderAccountProxy, guard ports.ProviderAccountSessionGuard, key []byte, endpoint string, newID func() string) *Service {
	return &Service{store: store, proxy: proxy, guard: guard, gate: make(chan struct{}, 1), ticketKey: append([]byte(nil), key...), endpoint: endpoint, newID: newID, usageCache: make(map[string]cachedUsage)}
}

// AccountUsages returns safe, best-effort quota summaries for signed-in
// accounts. Account inventory remains usable when a provider cannot report
// usage, so each failed lookup becomes an explicit unavailable state.
func (s *Service) AccountUsages(ctx context.Context, accounts []domain.ProviderAccount) map[string]domain.ProviderAccountUsage {
	result := make(map[string]domain.ProviderAccountUsage)
	proxy, ok := s.proxy.(ports.ProviderAccountUsageProxy)
	if !ok {
		for _, account := range accounts {
			if account.CredentialRef != "" && account.AuthID != "" {
				result[account.ID] = domain.ProviderAccountUsage{Status: "unavailable", Message: "Usage unavailable", CheckedAt: time.Now().UTC()}
			}
		}
		return result
	}
	var resultMu sync.Mutex
	var wg sync.WaitGroup
	now := time.Now()
	for _, account := range accounts {
		if account.CredentialRef == "" || account.AuthID == "" {
			continue
		}
		key := account.ID + "\x00" + account.AuthID
		s.usageMu.Lock()
		cached, found := s.usageCache[key]
		s.usageMu.Unlock()
		if found && now.Before(cached.expiresAt) {
			result[account.ID] = cached.value
			continue
		}
		account := account
		wg.Add(1)
		go func() {
			defer wg.Done()
			lookupCtx, cancel := context.WithTimeout(ctx, providerUsageLookupTimeout)
			defer cancel()
			usage, err := proxy.FetchAccountUsage(lookupCtx, account.Provider, account.AuthID, account.CredentialRef)
			checkedAt := time.Now().UTC()
			ttl := providerUsageCacheTTL
			if err != nil {
				usage = domain.ProviderAccountUsage{Status: "unavailable", Message: "Usage unavailable", CheckedAt: checkedAt}
				ttl = providerUsageFailureTTL
			}
			if usage.Status == "" {
				usage.Status = "available"
			}
			if usage.CheckedAt.IsZero() {
				usage.CheckedAt = checkedAt
			}
			s.usageMu.Lock()
			s.usageCache[key] = cachedUsage{value: usage, expiresAt: time.Now().Add(ttl)}
			s.usageMu.Unlock()
			resultMu.Lock()
			result[account.ID] = usage
			resultMu.Unlock()
		}()
	}
	wg.Wait()
	return result
}

// Provider maps supported harnesses to their account provider.
func Provider(h domain.AgentHarness) string {
	switch h {
	case domain.HarnessCodex:
		return "codex"
	case domain.HarnessClaudeCode:
		return "claude"
	}
	return ""
}

// refreshSignInFailures re-reads which accounts CLIProxy reports as no longer
// signed in. When the helper cannot answer, the last verdict stands: an outage
// must never block a healthy account.
func (s *Service) refreshSignInFailures(ctx context.Context, force bool) {
	proxy, ok := s.proxy.(ports.ProviderAccountSignInProxy)
	if !ok {
		return
	}
	s.signInMu.Lock()
	due := force || time.Since(s.signInCheckedAt) >= providerSignInCheckInterval
	if due {
		s.signInCheckedAt = time.Now()
	}
	s.signInMu.Unlock()
	if !due {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, providerUsageLookupTimeout)
	defer cancel()
	failures, err := proxy.AccountSignInFailures(ctx)
	if err != nil {
		return
	}
	s.signInMu.Lock()
	changed := !maps.Equal(s.signInFailures, failures)
	s.signInFailures = failures
	s.signInMu.Unlock()
	if changed && s.onReadinessChange != nil {
		// Asynchronous: readiness may be the caller that triggered this read.
		go func() {
			s.onReadinessChange("codex")
			s.onReadinessChange("claude")
		}()
	}
}

func (s *Service) signInFailed(authID string) bool {
	s.signInMu.Lock()
	defer s.signInMu.Unlock()
	_, failed := s.signInFailures[authID]
	return authID != "" && failed
}

// signInEnding reports that the last reading of an account found its saved
// sign-in no longer renewing. The reading answers until a newer one replaces
// it, also after its cache time has passed.
func (s *Service) signInEnding(a domain.ProviderAccount) bool {
	s.usageMu.Lock()
	defer s.usageMu.Unlock()
	return a.AuthID != "" && s.usageCache[a.ID+"\x00"+a.AuthID].value.SignInEnding
}

// signInReplaceable reports whether a new sign-in may take the place of the
// account's saved one: CLIProxy no longer accepts it, or it has stopped
// renewing and will stop working.
func (s *Service) signInReplaceable(a domain.ProviderAccount) bool {
	return s.signInFailed(a.AuthID) || s.signInEnding(a)
}

// usable is eligible plus CLIProxy's verdict that the saved sign-in still works.
func (s *Service) usable(state domain.ProviderAccountState, id, provider string) error {
	if err := eligible(state, id, provider); err != nil {
		return err
	}
	if a, _ := account(state, id); s.signInFailed(a.AuthID) {
		return ports.ErrProviderLoginRequired
	}
	return nil
}

// AccountSignInFailures reports, by account ID, the accounts whose saved
// sign-in CLIProxy no longer accepts. force re-reads the helper now.
func (s *Service) AccountSignInFailures(ctx context.Context, accounts []domain.ProviderAccount, force bool) map[string]bool {
	result := make(map[string]bool)
	signedIn := false
	for _, a := range accounts {
		signedIn = signedIn || a.AuthID != ""
	}
	if !signedIn {
		return result
	}
	s.refreshSignInFailures(ctx, force)
	for _, a := range accounts {
		if s.signInFailed(a.AuthID) {
			result[a.ID] = true
		}
	}
	return result
}

// WarmAccounts runs once at daemon start, as the account service did before
// Account Manager: learn each account's sign-in state and take a first usage
// reading so the first screen does not wait for the providers.
func (s *Service) WarmAccounts(ctx context.Context) {
	state, _, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return
	}
	s.AccountSignInFailures(ctx, state.Accounts, true)
	s.AccountUsages(ctx, state.Accounts)
}

// AuthenticationReadiness is the central auth result for AO-managed
// providers. An account counts when it has a saved credential that CLIProxy
// has not reported as dead; the helper validates and refreshes credentials.
func (s *Service) AuthenticationReadiness(ctx context.Context, harness domain.AgentHarness, _ domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool) {
	provider := Provider(harness)
	if provider == "" {
		return domain.AgentAuthenticationObservation{}, false
	}
	attempted := time.Now().UTC()
	state, _, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return domain.AgentAuthenticationObservation{
			State: domain.AgentAuthenticationUnknown, Freshness: domain.AgentReadinessStale,
			AttemptedAt: &attempted, ReasonCode: domain.AgentReadinessReasonAuthCheckFailed,
			Reason: "Managed account status could not be read.",
		}, true
	}
	if signedInCount(state, provider) > 0 {
		s.refreshSignInFailures(ctx, false)
		for _, a := range state.Accounts {
			if a.Provider == provider && a.CredentialRef != "" && a.AuthID != "" && !s.signInFailed(a.AuthID) {
				return domain.AgentAuthenticationObservation{
					State: domain.AgentAuthenticationAuthorized, Freshness: domain.AgentReadinessFresh,
					CheckedAt: &attempted, AttemptedAt: &attempted, ReasonCode: domain.AgentReadinessReasonAuthorized,
					Reason: "A managed account is signed in.",
				}, true
			}
		}
		return domain.AgentAuthenticationObservation{
			State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh,
			CheckedAt: &attempted, AttemptedAt: &attempted, ReasonCode: domain.AgentReadinessReasonUnauthorized,
			Reason: "Sign in again through Account Manager to use this provider.",
		}, true
	}
	return domain.AgentAuthenticationObservation{
		State: domain.AgentAuthenticationUnauthorized, Freshness: domain.AgentReadinessFresh,
		CheckedAt: &attempted, AttemptedAt: &attempted, ReasonCode: domain.AgentReadinessReasonUnauthorized,
		Reason: "Sign in through Account Manager to use this provider.",
	}, true
}

// DiscoverModels uses the account-scoped catalogue registered by CLIProxyAPI.
// Cloud credential scopes deliberately return false so their existing
// control-plane discovery remains authoritative.
func (s *Service) DiscoverModels(ctx context.Context, harness domain.AgentHarness, scope string) (ports.AgentModelCatalog, bool, error) {
	provider := Provider(harness)
	if provider == "" || strings.HasPrefix(strings.TrimSpace(scope), "@cred:") {
		return ports.AgentModelCatalog{}, false, nil
	}
	proxy, ok := s.proxy.(ports.ProviderAccountModelsProxy)
	if !ok {
		return ports.AgentModelCatalog{}, true, errors.New("managed model discovery is unavailable")
	}
	state, pending, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return ports.AgentModelCatalog{}, true, err
	}
	if pending != nil {
		return ports.AgentModelCatalog{}, true, ports.ErrProviderAccountRecovery
	}
	id, found := primary(state, provider)
	// An account scope asks for one explicitly chosen account's catalogue;
	// every other scope shows the provider default's.
	if chosen, ok := ports.AccountFromModelCatalogScope(scope); ok {
		id, found = chosen, true
	}
	if !found || id == "" {
		return ports.AgentModelCatalog{}, true, ports.ErrProviderLoginRequired
	}
	entry, found := account(state, id)
	if !found || entry.Provider != provider {
		return ports.AgentModelCatalog{}, true, ports.ErrProviderAccountUnknown
	}
	if entry.CredentialRef == "" || entry.AuthID == "" {
		return ports.AgentModelCatalog{}, true, ports.ErrProviderLoginRequired
	}
	catalog, err := proxy.FetchAccountModels(ctx, provider, entry.AuthID)
	if err != nil {
		return ports.AgentModelCatalog{}, true, err
	}
	catalog.AgentID = string(harness)
	return catalog, true, nil
}

// ModelsFingerprint identifies the model list a scope's account currently has.
func (s *Service) ModelsFingerprint(ctx context.Context, harness domain.AgentHarness, scope string) (string, bool, error) {
	catalog, handled, err := s.DiscoverModels(ctx, harness, scope)
	if !handled || err != nil {
		return "", handled, err
	}
	sum := sha256.New()
	for _, model := range catalog.Models {
		sum.Write([]byte(model.ID))
		// Reasoning levels are part of what the list offers: a stored list
		// without them is replaced as soon as the account reports them.
		for _, effort := range model.Efforts {
			sum.Write([]byte{1})
			sum.Write([]byte(effort))
		}
		sum.Write([]byte{0})
	}
	// The number names what AO keeps from the list. A stored list made under an
	// earlier rule (one that marked the first entry as the default) is replaced
	// the next time it is read.
	return "account-models-2:" + hex.EncodeToString(sum.Sum(nil)), true, nil
}

// SetReadinessInvalidator keeps the shared readiness cache aligned with
// account-manager login, logout, and native-account adoption changes.
func (s *Service) SetReadinessInvalidator(invalidate func(string)) { s.onReadinessChange = invalidate }

func (s *Service) lock(ctx context.Context) (func(), error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case s.gate <- struct{}{}:
		return func() { <-s.gate }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *Service) acquireAccountMutation(ctx context.Context, ids []domain.SessionID) (func(), error) {
	if len(ids) == 0 {
		return func() {}, nil
	}
	if s.guard == nil {
		return nil, ports.ErrProviderAccountBusy
	}
	waitCtx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	return s.guard.AcquireAccountMutation(waitCtx, ids)
}

// State reads durable account facts.
func (s *Service) State(ctx context.Context) (domain.ProviderAccountState, error) {
	state, _, err := s.store.LoadProviderAccountState(ctx)
	if err == nil {
		fillMissingDisplayNames(&state)
	}
	return state, err
}

func fillMissingDisplayNames(state *domain.ProviderAccountState) {
	used := make(map[string]bool, len(state.Accounts))
	for _, account := range state.Accounts {
		if account.DisplayName != "" {
			used[account.DisplayName] = true
		}
	}
	for i := range state.Accounts {
		if state.Accounts[i].DisplayName == "" {
			state.Accounts[i].DisplayName = nextAccountName(state.Accounts[i].Provider, state.Accounts[i].ID, used)
			used[state.Accounts[i].DisplayName] = true
		}
	}
}

func nextAccountName(provider, id string, used map[string]bool) string {
	base := domain.GeneratedProviderAccountName(provider, id)
	if !used[base] {
		return base
	}
	for n := 2; ; n++ {
		candidate := fmt.Sprintf("%s %d", base, n)
		if !used[candidate] {
			return candidate
		}
	}
}

// Rename changes only the local display label; routing continues to use the
// account's stable ID.
func (s *Service) Rename(ctx context.Context, id, displayName string) error {
	name := strings.Join(strings.Fields(displayName), " ")
	if len([]rune(name)) == 0 || len([]rune(name)) > 80 {
		return ports.ErrProviderAccountNameInvalid
	}
	return s.mutate(ctx, func(state *domain.ProviderAccountState) (string, error) {
		for i := range state.Accounts {
			if state.Accounts[i].ID == id {
				state.Accounts[i].DisplayName = name
				return "", nil
			}
		}
		return "", ports.ErrProviderAccountUnknown
	})
}
func account(state domain.ProviderAccountState, id string) (domain.ProviderAccount, bool) {
	for _, a := range state.Accounts {
		if a.ID == id {
			return a, true
		}
	}
	return domain.ProviderAccount{}, false
}
func signedInCount(state domain.ProviderAccountState, provider string) int {
	count := 0
	for _, a := range state.Accounts {
		if a.Provider == provider && a.CredentialRef != "" && a.AuthID != "" {
			count++
		}
	}
	return count
}
func primary(state domain.ProviderAccountState, provider string) (string, bool) {
	for _, p := range state.Primaries {
		if p.Provider == provider {
			return p.PrimaryID, true
		}
	}
	return "", false
}
func setPrimary(state *domain.ProviderAccountState, provider, id string) {
	for i, p := range state.Primaries {
		if p.Provider == provider {
			state.Primaries[i].PrimaryID = id
			return
		}
	}
	state.Primaries = append(state.Primaries, domain.ProviderPrimary{Provider: provider, PrimaryID: id})
}
func eligible(state domain.ProviderAccountState, id, provider string) error {
	a, found := account(state, id)
	if !found {
		return ports.ErrProviderAccountUnknown
	}
	if a.Provider != provider {
		return ports.ErrProviderAccountIncompatible
	}
	if a.CredentialRef == "" || a.AuthID == "" {
		return ports.ErrProviderLoginRequired
	}
	return nil
}

// ResolveAccount chooses an explicit account or the matching primary for a new
// session. Codex and Claude are managed providers once this build is running:
// native credentials are retained only for sessions created before adoption,
// and must never be an implicit fallback for a new session.
func (s *Service) ResolveAccount(ctx context.Context, harness domain.AgentHarness, explicit string) (string, bool, error) {
	state, pending, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return "", false, err
	}
	if pending != nil {
		return "", false, ports.ErrProviderAccountRecovery
	}
	provider := Provider(harness)
	if provider == "" {
		if explicit != "" {
			return "", false, ports.ErrProviderAccountIncompatible
		}
		return "", false, nil
	}
	id, _ := primary(state, provider)
	if explicit != "" {
		id = explicit
	}
	if id == "" {
		return "", true, ports.ErrProviderLoginRequired
	}
	s.refreshSignInFailures(ctx, false)
	if err := s.usable(state, id, provider); err != nil {
		return "", true, err
	}
	return id, true, nil
}

// SessionAccount reads the saved assignment without changing native sessions.
func (s *Service) SessionAccount(ctx context.Context, id domain.SessionID) (domain.ProviderSessionRoute, bool, error) {
	state, err := s.State(ctx)
	if err != nil {
		return domain.ProviderSessionRoute{}, false, err
	}
	for _, r := range state.Routes {
		if r.SessionID == id {
			return r, true, nil
		}
	}
	return domain.ProviderSessionRoute{}, false, nil
}
func (s *Service) ticket(id domain.SessionID) string {
	mac := hmac.New(sha256.New, s.ticketKey)
	mac.Write([]byte("ao-provider-session\x00" + string(id)))
	return hex.EncodeToString(mac.Sum(nil))
}

// LaunchAccountEnv provides the stable private endpoint and ticket for a managed session.
func (s *Service) LaunchAccountEnv(ctx context.Context, id domain.SessionID) (map[string]string, error) {
	route, managed, err := s.SessionAccount(ctx, id)
	if err != nil || !managed {
		return nil, err
	}
	if len(s.ticketKey) < 32 || s.endpoint == "" {
		return nil, errors.New("proxy launch configuration unavailable")
	}
	ticket := s.ticket(id)
	// Even waiting sessions retain their endpoint/ticket and cannot fall back to
	// ambient credentials. Re-login changes the mapping, not their environment.
	if route.Provider == "codex" {
		return map[string]string{"AO_PROXY_ENDPOINT": s.endpoint, "AO_PROXY_TICKET": ticket}, nil
	}
	return map[string]string{"AO_PROXY_ENDPOINT": "", "AO_PROXY_TICKET": "", "ANTHROPIC_BASE_URL": s.endpoint, "ANTHROPIC_AUTH_TOKEN": ticket, "ANTHROPIC_API_KEY": "", "CLAUDE_CODE_OAUTH_TOKEN": "", "CLAUDE_CODE_USE_BEDROCK": "", "CLAUDE_CODE_USE_VERTEX": "", "CLAUDE_CODE_USE_FOUNDRY": ""}, nil
}
func snapshot(state domain.ProviderAccountState) ports.ProviderRouteSnapshot {
	result := ports.ProviderRouteSnapshot{Revision: state.Revision, Routes: make([]ports.ProviderRoute, 0, len(state.Routes))}
	for _, a := range state.Accounts {
		if a.CredentialRef != "" && a.AuthID != "" {
			result.AuthIDs = append(result.AuthIDs, a.AuthID)
		}
	}
	for _, r := range state.Routes {
		authID := ""
		if a, ok := account(state, r.AccountID); ok {
			authID = a.AuthID
		}
		result.Routes = append(result.Routes, ports.ProviderRoute{SessionID: r.SessionID, TicketHash: r.TicketHash, Provider: r.Provider, AuthID: authID})
	}
	return result
}
func (s *Service) reconcile(ctx context.Context, guarded bool) error {
	state, pending, err := s.store.LoadProviderAccountState(ctx)
	if err != nil || pending == nil {
		return err
	}
	// Also invalidate after recovery or a partial commit, not just a successful
	// user action: durable authentication facts may already have changed.
	if s.onReadinessChange != nil {
		defer s.onReadinessChange("codex")
		defer s.onReadinessChange("claude")
	}
	if pending.Next.Revision != state.Revision && pending.Next.Revision != state.Revision+1 {
		return ports.ErrProviderAccountRecovery
	}
	if !guarded && state.Revision != pending.Next.Revision {
		affected := mutationSessions(snapshot(state), snapshot(pending.Next), pending.RequestBoundary)
		if len(affected) > 0 {
			done, guardErr := s.acquireAccountMutation(ctx, affected)
			if guardErr != nil {
				return guardErr
			}
			defer done()
		}
	}
	next := snapshot(pending.Next)
	next.RequestBoundary = pending.RequestBoundary
	if err = s.proxy.ApplyRoutes(ctx, next); err != nil {
		// A helper admission refusal proves that it changed no routes. Clear
		// this uncommitted intent so an idle retry requires a new user action.
		// Failed cleanup leaves the operation unresolved, not safely cancelled.
		if errors.Is(err, ports.ErrProviderAccountBusy) && state.Revision != pending.Next.Revision {
			if cleanupErr := s.store.FinishProviderAccountIntent(ctx, state.Revision); cleanupErr != nil {
				return fmt.Errorf("abort account routing: %w", errors.Join(ports.ErrProviderAccountRecovery, cleanupErr))
			}
		}
		return fmt.Errorf("apply account routing: %w", err)
	}
	if state.Revision != pending.Next.Revision {
		if err = s.store.CommitProviderAccountIntent(ctx, state.Revision); err != nil {
			return fmt.Errorf("commit account routing: %w", err)
		}
	}
	if pending.DeleteCredential != "" {
		if err = s.proxy.DeleteCredential(ctx, pending.DeleteCredential); err != nil {
			return fmt.Errorf("remove saved credential: %w", err)
		}
	}
	return s.store.FinishProviderAccountIntent(ctx, pending.Next.Revision)
}

// Recover resolves a durably admitted operation whose outcome is still pending.
func (s *Service) Recover(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	release, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.reconcile(ctx, false)
}

// RestoreHost recovers an admitted operation and verifies the helper's effective
// routes, restarting only a confirmed dead owned helper through the proxy port.
func (s *Service) RestoreHost(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	release, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	if err := s.reconcile(ctx, false); err != nil {
		return err
	}
	state, _, err := s.store.LoadProviderAccountState(ctx)
	if err != nil || len(state.Primaries) == 0 {
		return err
	}
	return s.proxy.ApplyRoutes(ctx, snapshot(state))
}

func (s *Service) mutate(ctx context.Context, change func(*domain.ProviderAccountState) (string, error)) error {
	return s.mutateWithBoundary(ctx, false, change)
}

// mutateWithBoundary, with requestBoundary set, lets affected sessions pick up
// the change on their next request instead of first being idle, even when the
// change also removes a credential.
func (s *Service) mutateWithBoundary(ctx context.Context, requestBoundary bool, change func(*domain.ProviderAccountState) (string, error)) error {
	ctx, cancel := context.WithTimeout(ctx, providerAccountOperationTimeout)
	defer cancel()
	release, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	return s.mutateLocked(ctx, requestBoundary, change)
}

// mutateLocked commits through the existing recovery journal with gate held.
func (s *Service) mutateLocked(ctx context.Context, requestBoundary bool, change func(*domain.ProviderAccountState) (string, error)) error {
	if err := s.reconcile(ctx, false); err != nil {
		return err
	}
	state, _, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return err
	}
	oldRevision := state.Revision
	before := snapshot(state)
	deletion, err := change(&state)
	if err != nil {
		return err
	}
	after := snapshot(state)
	requestBoundary = requestBoundary || deletion == ""
	affected := mutationSessions(before, after, requestBoundary)
	if len(affected) > 0 {
		done, err := s.acquireAccountMutation(ctx, affected)
		if err != nil {
			return err
		}
		defer done()
	}
	state.Revision++
	if err := s.store.SaveProviderAccountIntent(ctx, oldRevision, domain.ProviderAccountIntent{Next: state, DeleteCredential: deletion, RequestBoundary: requestBoundary}); err != nil {
		return err
	}
	return s.reconcile(ctx, true)
}

// RecordCredential accepts an inventory-verified OAuth, imported, or API-key
// credential. API-key references are opaque helper-owned values and therefore
// are allowed to use the config: namespace.
func (s *Service) RecordCredential(ctx context.Context, verified ports.VerifiedProviderLogin, reloginID string) (string, error) {
	provider, email, credentialRef, authID := verified.Provider, verified.Email, verified.CredentialRef, verified.AuthID
	validRef := filepath.Base(credentialRef) == credentialRef && !strings.ContainsAny(credentialRef, "/\\") && credentialRef != "." && credentialRef != ".."
	if strings.HasPrefix(credentialRef, "config:") {
		validRef = len(credentialRef) > len("config:") && !strings.ContainsAny(credentialRef, "/\\")
	}
	if (provider != "codex" && provider != "claude") || strings.TrimSpace(email) == "" || credentialRef == "" || authID == "" || !validRef {
		return "", fmt.Errorf("invalid verified provider account: %w", ports.ErrProviderAccountIncompatible)
	}
	kind := strings.TrimSpace(verified.Kind)
	if kind == "" {
		kind = "oauth"
	}
	id := reloginID
	if id == "" {
		id = s.newID()
	}
	err := s.mutateWithBoundary(ctx, true, func(state *domain.ProviderAccountState) (string, error) {
		fillMissingDisplayNames(state)
		used := make(map[string]bool, len(state.Accounts))
		for _, account := range state.Accounts {
			used[account.DisplayName] = true
		}
		replaced := ""
		if old, ok := account(*state, id); ok {
			if old.Provider != provider || !strings.EqualFold(old.Email, email) {
				return "", ports.ErrProviderAccountIncompatible
			}
			if old.CredentialRef != "" {
				if old.CredentialRef == credentialRef && old.AuthID == authID {
					return "", nil
				}
				if !s.signInReplaceable(old) {
					return "", fmt.Errorf("account is already signed in: %w", ports.ErrProviderAccountConflict)
				}
				// The saved sign-in is dead or dying, so the new one replaces
				// it; the account's sessions keep their assignment.
				replaced = old.CredentialRef
			}
			for i, a := range state.Accounts {
				if a.ID == id {
					state.Accounts[i] = domain.ProviderAccount{ID: id, Provider: provider, DisplayName: old.DisplayName, Email: email, Kind: kind, CredentialRef: credentialRef, AuthID: authID}
				}
			}
		} else {
			if reloginID != "" {
				return "", ports.ErrProviderAccountUnknown
			}
			for _, a := range state.Accounts {
				if a.Provider == provider && strings.EqualFold(a.Email, email) {
					if a.CredentialRef == credentialRef && a.AuthID == authID {
						id = a.ID
						return "", nil
					}
					return "", fmt.Errorf("account already exists; sign in to its existing entry: %w", ports.ErrProviderAccountConflict)
				}
			}
			state.Accounts = append(state.Accounts, domain.ProviderAccount{ID: id, Provider: provider, DisplayName: nextAccountName(provider, id, used), Email: email, Kind: kind, CredentialRef: credentialRef, AuthID: authID})
		}
		// A sign-in rewrites the account; keep its Global mark true to the rule.
		markGlobal(state, provider)
		current, _ := primary(*state, provider)
		if current == "" {
			setPrimary(state, provider, id)
			for i, r := range state.Routes {
				if r.Provider == provider && r.AccountID == "" {
					state.Routes[i].AccountID = id
				}
			}
		}
		return replaced, nil
	})
	return id, err
}

// SetPrimary changes the default for new sessions. Call SetPrimaryWithOptions
// when the caller explicitly chooses to move existing provider routes too.
func (s *Service) SetPrimary(ctx context.Context, id string) error {
	return s.SetPrimaryWithOptions(ctx, id, false)
}

// SetPrimaryWithOptions changes the default and optionally rebinds routes that
// still point at the previous provider default. Rebinding is applied at the next
// request boundary, so an in-flight request keeps its current credentials.
func (s *Service) SetPrimaryWithOptions(ctx context.Context, id string, moveExisting bool) error {
	s.refreshSignInFailures(ctx, false)
	return s.mutate(ctx, func(state *domain.ProviderAccountState) (string, error) {
		a, ok := account(*state, id)
		if !ok {
			return "", ports.ErrProviderAccountUnknown
		}
		if err := s.usable(*state, id, a.Provider); err != nil {
			return "", err
		}
		previous, _ := primary(*state, a.Provider)
		if moveExisting && previous != "" {
			for i, r := range state.Routes {
				if r.Provider == a.Provider && r.AccountID == previous {
					state.Routes[i].AccountID = id
				}
			}
		}
		setPrimary(state, a.Provider, id)
		return "", nil
	})
}

// AssignAccount creates a session ticket bound to an eligible account.
func (s *Service) AssignAccount(ctx context.Context, id domain.SessionID, harness domain.AgentHarness, accountID string) error {
	provider := Provider(harness)
	return s.mutate(ctx, func(state *domain.ProviderAccountState) (string, error) {
		if provider == "" {
			return "", ports.ErrProviderAccountIncompatible
		}
		if err := s.usable(*state, accountID, provider); err != nil {
			return "", err
		}
		for _, r := range state.Routes {
			if r.SessionID == id {
				return "", fmt.Errorf("session already has an account route: %w", ports.ErrProviderAccountConflict)
			}
		}
		sum := sha256.Sum256([]byte(s.ticket(id)))
		state.Routes = append(state.Routes, domain.ProviderSessionRoute{SessionID: id, Provider: provider, AccountID: accountID, TicketHash: hex.EncodeToString(sum[:])})
		return "", nil
	})
}

// Switch retains the ticket; provider rebinds apply at the next request.
func (s *Service) Switch(ctx context.Context, id domain.SessionID, target string) error {
	s.refreshSignInFailures(ctx, false)
	return s.mutate(ctx, func(state *domain.ProviderAccountState) (string, error) {
		for i, r := range state.Routes {
			if r.SessionID == id {
				if err := s.usable(*state, target, r.Provider); err != nil {
					return "", err
				}
				state.Routes[i].AccountID = target
				return "", nil
			}
		}
		return "", fmt.Errorf("older native sessions cannot change managed accounts: %w", ports.ErrProviderAccountIncompatible)
	})
}

// Remove signs out or removes an account and reassigns its affected sessions.
func (s *Service) Remove(ctx context.Context, id, replacement string, signOut bool) error {
	return s.mutate(ctx, func(state *domain.ProviderAccountState) (string, error) {
		a, ok := account(*state, id)
		if !ok {
			return "", ports.ErrProviderAccountUnknown
		}
		current, _ := primary(*state, a.Provider)
		if current == id {
			hasOther := false
			for _, other := range state.Accounts {
				if other.ID != id && other.Provider == a.Provider && other.CredentialRef != "" {
					hasOther = true
				}
			}
			if hasOther {
				if replacement == "" || replacement == id {
					return "", ports.ErrProviderPrimaryRequired
				}
				if err := s.usable(*state, replacement, a.Provider); err != nil {
					return "", err
				}
				current = replacement
			} else {
				current = ""
			}
			setPrimary(state, a.Provider, current)
		}
		for i, r := range state.Routes {
			if r.AccountID == id {
				state.Routes[i].AccountID = current
			}
		}
		accounts := state.Accounts[:0]
		for _, entry := range state.Accounts {
			if entry.ID == id {
				if !signOut {
					continue
				}
				entry.CredentialRef = ""
				entry.AuthID = ""
			}
			accounts = append(accounts, entry)
		}
		state.Accounts = accounts
		return a.CredentialRef, nil
	})
}

func changedSessions(before, after ports.ProviderRouteSnapshot) []domain.SessionID {
	var affected []domain.SessionID
	for _, old := range before.Routes {
		found := false
		for _, next := range after.Routes {
			if old.SessionID == next.SessionID {
				found = true
				if old.AuthID != next.AuthID {
					affected = append(affected, old.SessionID)
				}
				break
			}
		}
		if !found {
			affected = append(affected, old.SessionID)
		}
	}
	return affected
}

// Only a non-revoking provider rebind may skip native idle admission. Destructive
// mutations still fence workers/reviewers; the helper separately drains auth leases.
func mutationSessions(before, after ports.ProviderRouteSnapshot, requestBoundary bool) []domain.SessionID {
	if requestBoundary {
		for i, old := range before.Routes {
			for _, next := range after.Routes {
				if old.SessionID == next.SessionID && (old.Provider == "codex" || old.Provider == "claude") && next.Provider == old.Provider && next.TicketHash == old.TicketHash && next.AuthID != "" {
					before.Routes[i].AuthID = next.AuthID
				}
			}
		}
	}
	return changedSessions(before, after)
}

// RecoveryRequired reports whether a durable account operation remains unfinished.
func (s *Service) RecoveryRequired(ctx context.Context) (bool, error) {
	_, pending, err := s.store.LoadProviderAccountState(ctx)
	return pending != nil, err
}

// ForgetAccount revokes a deleted seed session's ticket. Archived sessions keep
// their assignment because they may be restored with native history.
func (s *Service) ForgetAccount(ctx context.Context, id domain.SessionID) error {
	_, managed, err := s.SessionAccount(ctx, id)
	if err != nil || !managed {
		return err
	}
	return s.mutate(ctx, func(state *domain.ProviderAccountState) (string, error) {
		routes := state.Routes[:0]
		for _, r := range state.Routes {
			if r.SessionID != id {
				routes = append(routes, r)
			}
		}
		state.Routes = routes
		return "", nil
	})
}

// discardLogin deletes a rejected login only when no effective or pending account uses its credential.
func (s *Service) discardLogin(ctx context.Context, login ports.VerifiedProviderLogin) error {
	if login.CredentialRef == "" {
		return nil
	}
	release, err := s.lock(ctx)
	if err != nil {
		return err
	}
	defer release()
	state, pending, err := s.store.LoadProviderAccountState(ctx)
	if err != nil {
		return err
	}
	entries := state.Accounts
	if pending != nil {
		entries = append(entries, pending.Next.Accounts...)
	}
	for _, a := range entries {
		if a.CredentialRef == login.CredentialRef {
			return nil
		}
	}
	return s.proxy.DeleteCredential(ctx, login.CredentialRef)
}
