package ports

import (
	"context"
	"errors"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

// ErrProviderAccountConflict reports an incompatible concurrent or duplicate account operation.
var ErrProviderAccountConflict = errors.New("account operation conflicts with current state")

// ErrProviderLoginUnknown reports a login attempt that is no longer known.
var ErrProviderLoginUnknown = errors.New("login attempt not found; sign in again")

// ErrProviderLoginCallbackBusy reports another login occupying the provider callback port.
var ErrProviderLoginCallbackBusy = errors.New("login callback port is in use; finish the other login and retry")

// ErrProviderLoginRequired reports an account without usable saved credentials.
var ErrProviderLoginRequired = errors.New("provider account login required")

// ErrProviderAccountBusy refuses destructive changes while affected sessions are working.
var ErrProviderAccountBusy = errors.New("this account is still in use; try again when its sessions have finished")

// ErrProviderPrimaryRequired requires a replacement before removing a usable primary.
var ErrProviderPrimaryRequired = errors.New("choose a replacement primary account first")

// ErrProviderAccountUnknown reports an account absent from the catalogue.
var ErrProviderAccountUnknown = errors.New("provider account not found")

// ErrProviderAccountNameInvalid reports an empty or overly long display name.
var ErrProviderAccountNameInvalid = errors.New("account name must be between 1 and 80 characters")

// ErrProviderAccountIncompatible reports an account or operation for a different provider.
var ErrProviderAccountIncompatible = errors.New("account does not match the session provider")

// ErrProviderAccountRecovery reports a pending or inconsistent routing operation.
var ErrProviderAccountRecovery = errors.New("provider account operation requires recovery")

// ProviderAccountStore persists routing facts and the recoverable mutation journal.
type ProviderAccountStore interface {
	LoadProviderAccountState(context.Context) (domain.ProviderAccountState, *domain.ProviderAccountIntent, error)
	SaveProviderAccountIntent(context.Context, int64, domain.ProviderAccountIntent) error
	CommitProviderAccountIntent(context.Context, int64) error
	FinishProviderAccountIntent(context.Context, int64) error
}

// ProviderRouteSnapshot is the complete revisioned helper routing table.
type ProviderRouteSnapshot struct {
	Revision        int64           `json:"revision"`
	Routes          []ProviderRoute `json:"routes"`
	AuthIDs         []string        `json:"auth_ids,omitempty"`
	RequestBoundary bool            `json:"request_boundary,omitempty"`
}

// ProviderRoute contains a ticket hash and an exact upstream account identity.
type ProviderRoute struct {
	SessionID  domain.SessionID `json:"session_id"`
	TicketHash string           `json:"ticket_hash"`
	Provider   string           `json:"provider"`
	AuthID     string           `json:"auth_id"`
}

// ProviderAccountProxy acknowledges routing changes and deletes saved upstream credentials.
type ProviderAccountProxy interface {
	ApplyRoutes(context.Context, ProviderRouteSnapshot) error
	DeleteCredential(context.Context, string) error
}

// ProviderAccountUsageProxy reads safe, provider-normalized quota summaries.
// It is optional so older helper implementations can still manage accounts.
type ProviderAccountUsageProxy interface {
	FetchAccountUsage(context.Context, string, string, string) (domain.ProviderAccountUsage, error)
}

// ProviderAccountActionProxy runs the account actions that are one background
// request to the provider or the helper. Accounts are named by the helper's
// opaque identity.
type ProviderAccountActionProxy interface {
	// UseAccountReset spends one usage-limit reset and returns its outcome.
	// requestID identifies the attempt so a repeat cannot spend a second one.
	UseAccountReset(ctx context.Context, provider, authID, requestID string) (string, error)
	// ResumeAccount stops the helper holding an account back after a refusal.
	ResumeAccount(ctx context.Context, provider, authID string) error
	// RefreshAccountSignIn renews the saved sign-in now.
	RefreshAccountSignIn(ctx context.Context, provider, authID string) error
}

// ErrProviderAccountActionUnavailable reports an account action this build or
// this kind of account cannot perform.
var ErrProviderAccountActionUnavailable = errors.New("this account action is unavailable")

// ProviderAccountSignInProxy reads CLIProxy's own per-account state and returns
// the accounts whose saved sign-in it no longer accepts, keyed by the helper's
// account identity, with CLIProxy's short reason.
type ProviderAccountSignInProxy interface {
	AccountSignInFailures(context.Context) (map[string]string, error)
}

// ProviderCredential is one sign-in the helper holds on disk. Name is what an
// account's CredentialRef carries.
type ProviderCredential struct {
	Name       string
	Provider   string
	ModifiedAt time.Time
}

// ProviderCredentialInventory lists the sign-ins the helper holds, so AO can
// remove the ones no account uses.
type ProviderCredentialInventory interface {
	ListCredentials(context.Context) ([]ProviderCredential, error)
}

// ProviderAccountModelsProxy reads the account-scoped model catalogue already
// maintained by CLIProxyAPI. The account id is an opaque helper identity; raw
// provider credentials never cross this boundary.
type ProviderAccountModelsProxy interface {
	FetchAccountModels(context.Context, string, string) (AgentModelCatalog, error)
}

// ProviderAccountSessionGuard fences affected sessions while their account mappings change.
type ProviderAccountSessionGuard interface {
	AcquireAccountMutation(context.Context, []domain.SessionID) (func(), error)
}

// ProviderAccountRouting resolves defaults and prepares managed session launches.
type ProviderAccountRouting interface {
	ResolveAccount(context.Context, domain.AgentHarness, string) (string, bool, error)
	AssignAccount(context.Context, domain.SessionID, domain.AgentHarness, string) error
	SessionAccount(context.Context, domain.SessionID) (domain.ProviderSessionRoute, bool, error)
	LaunchAccountEnv(context.Context, domain.SessionID) (map[string]string, error)
}

// ManagedProviderReadiness supplies the authentication result for providers
// whose credentials are owned by AO's account manager. Native harness checks
// remain the fallback for every other provider.
type ManagedProviderReadiness interface {
	AuthenticationReadiness(context.Context, domain.AgentHarness, domain.AgentReadinessPurpose) (domain.AgentAuthenticationObservation, bool)
}

// ManagedProviderModelDiscovery supplies model catalogues for local providers
// owned by Account Manager. The scope is passed so cloud credential catalogues
// can continue using their existing control-plane path.
type ManagedProviderModelDiscovery interface {
	DiscoverModels(context.Context, domain.AgentHarness, string) (AgentModelCatalog, bool, error)
	// ModelsFingerprint changes when the catalogue DiscoverModels would return
	// changes. It stands in for the native discovery fingerprint, so the cache's
	// "refresh when the inputs change" rule keeps working for managed providers.
	ModelsFingerprint(context.Context, domain.AgentHarness, string) (string, bool, error)
}

// ProviderLogin tracks one upstream login attempt, including private OAuth state.
type ProviderLogin struct {
	ID        string `json:"id"`
	Provider  string `json:"provider"`
	Mode      string `json:"mode,omitempty"`
	State     string `json:"state"`
	URL       string `json:"url"`
	Code      string `json:"code,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
	Status    string `json:"status"`
	AccountID string `json:"account_id"`
}

// ProviderLoginInput carries one non-browser credential input to the private
// helper. CredentialJSON and APIKey never leave the daemon/helper boundary.
type ProviderLoginInput struct {
	APIKey         string
	BaseURL        string
	Label          string
	CredentialJSON string
}

// VerifiedProviderLogin contains identity obtained from the successful upstream credential.
type VerifiedProviderLogin struct {
	Provider      string `json:"provider"`
	Email         string `json:"email"`
	Kind          string `json:"kind,omitempty"`
	CredentialRef string `json:"credential_ref"`
	AuthID        string `json:"auth_id"`
}

// NativeProviderCredential stays inside the daemon/helper boundary. Fingerprint
// identifies a native login snapshot without storing its tokens in AO's database.
type NativeProviderCredential struct {
	Fingerprint    string
	CredentialJSON string
	// Email is whose login this is, when the credential itself says.
	Email string
}

// ProviderNativeAccountSource reads native logins without modifying them and
// imports a new snapshot through CLIProxy's credential management API.
type ProviderNativeAccountSource interface {
	ReadNativeAccount(context.Context, string) (NativeProviderCredential, error)
	ImportNativeAccount(context.Context, string, NativeProviderCredential) (VerifiedProviderLogin, error)
}

// NativeProviderAPIKey is the API key this computer's own agent is set up to
// use, with the address it is used against. It stays inside the daemon/helper
// boundary. Fingerprint identifies the pair without storing the key in AO's
// database.
type NativeProviderAPIKey struct {
	Fingerprint string
	APIKey      string
	BaseURL     string
}

// ProviderNativeAPIKeySource reads that key without changing where it is kept
// and adds it through CLIProxy's key management.
type ProviderNativeAPIKeySource interface {
	ReadNativeAPIKey(context.Context, string) (NativeProviderAPIKey, error)
	ImportNativeAPIKey(context.Context, string, NativeProviderAPIKey) (VerifiedProviderLogin, error)
}

// ProviderAccountLoginProxy starts, verifies, and cancels upstream logins.
type ProviderAccountLoginProxy interface {
	StartAccountLogin(context.Context, string, string) (ProviderLogin, error)
	AccountLoginStatus(context.Context, ProviderLogin) (string, error)
	CancelAccountLogin(context.Context, ProviderLogin) error
	VerifiedAccountLogin(context.Context, string) (VerifiedProviderLogin, error)
}

// ProviderAccountLoginModes is the optional extension used by the account
// panel for device login, credential-file import, and API-key accounts.
type ProviderAccountLoginModes interface {
	StartAccountLoginMode(context.Context, string, string, string, ProviderLoginInput) (ProviderLogin, error)
}
