package agentaccounts

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// UILogin runs a provider CLI's own sign-in in the background for an account
// folder, the way the harness's in-app sign-in does for the computer's own
// login: the app shows its link, device code and code field instead of a
// terminal. env points the CLI at the account's folder.
type UILogin interface {
	StartAccountLogin(ctx context.Context, harness domain.AgentHarness, method, secret string, env map[string]string) (string, error)
	CancelAccountLogin(ctx context.Context, harness domain.AgentHarness, id string) error
}

// SetUILogin late-binds the in-app sign-in, which the daemon builds after the
// account catalog.
func (s *Service) SetUILogin(login UILogin) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.uiLogin = login
}

// setupTokenMethod saves a `claude setup-token` token as an account's sign-in;
// no CLI sign-in runs.
const setupTokenMethod = "setup_token"

// methodKind is the kind of account a sign-in method makes, and whether the
// provider's accounts take that method at all. An API key method of Claude
// Code is not here: AddAPIKeyAccount saves those.
func methodKind(p domain.AgentAccountProvider, method string) (domain.AgentAccountKind, bool) {
	switch p {
	case domain.AgentAccountProviderClaude:
		switch method {
		case "browser", "sso":
			return domain.AgentAccountKindSubscription, true
		case "console":
			return domain.AgentAccountKindMetered, true
		case setupTokenMethod:
			return domain.AgentAccountKindSetupToken, true
		}
	case domain.AgentAccountProviderCodex:
		switch method {
		case "browser", "device_code", "access_token":
			return domain.AgentAccountKindSubscription, true
		case "api_key":
			return domain.AgentAccountKindMetered, true
		}
	}
	return "", false
}

// accountKind is account's kind, a subscription for one saved before kinds.
func accountKind(account domain.AgentAccount) domain.AgentAccountKind {
	if account.Kind == "" {
		return domain.AgentAccountKindSubscription
	}
	return account.Kind
}

// loginKind is the kind of account a pending sign-in makes: its method's, or,
// for a terminal sign-in again, the account's own.
func loginKind(op *login) domain.AgentAccountKind {
	if op.method != "" {
		if kind, ok := methodKind(op.provider, op.method); ok {
			return kind
		}
	}
	if op.account != nil {
		return accountKind(*op.account)
	}
	return domain.AgentAccountKindSubscription
}

// harnessOf is the harness whose CLI signs in an account of p.
func harnessOf(p domain.AgentAccountProvider) domain.AgentHarness {
	if p == domain.AgentAccountProviderCodex {
		return domain.HarnessCodex
	}
	return domain.HarnessClaudeCode
}

// startUILogin starts the CLI's own sign-in for method in a new account folder,
// or in account's own folder, in the background. VerifyLogin saves it once the
// app sees the sign-in finish.
func (s *Service) startUILogin(ctx context.Context, p domain.AgentAccountProvider, account *domain.AgentAccount, req LoginRequest) (LoginStart, error) {
	provider := s.providers[p]
	if provider == nil {
		return LoginStart{}, ports.ErrAgentAccountProviderMismatch
	}
	if _, ok := methodKind(p, req.Method); !ok {
		return LoginStart{}, ports.ErrAgentAccountMethodInvalid
	}
	if req.Method == setupTokenMethod {
		return s.saveSetupToken(ctx, p, account, req)
	}
	// Codex saves an API key without asking OpenAI, so a mistyped one would
	// only fail at the first turn.
	if p == domain.AgentAccountProviderCodex && req.Method == "api_key" {
		if err := s.checkKey(ctx, p, strings.TrimSpace(req.Secret)); err != nil {
			return LoginStart{}, err
		}
	}
	s.mu.Lock()
	ui := s.uiLogin
	s.mu.Unlock()
	if ui == nil {
		return LoginStart{}, ports.ErrAgentAccountSignInUnavailable
	}
	id := uuid.NewString()
	home := filepath.Join(s.root, string(p), id)
	if account != nil {
		home = account.Home
	}
	// A saved account's folder is never removed here; only a new one is.
	discard := func() {
		if account == nil {
			s.removeFolder(home)
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
	authOp, err := ui.StartAccountLogin(ctx, harnessOf(p), req.Method, req.Secret, provider.LaunchEnv(home))
	if err != nil {
		discard()
		return LoginStart{}, err
	}
	s.mu.Lock()
	s.logins[id] = &login{id: id, provider: p, home: home, account: account, method: req.Method, authOp: authOp, name: cleanAPIKeyName(req.Name)}
	s.mu.Unlock()
	return LoginStart{OperationID: id, Provider: p, AuthOperationID: authOp}, nil
}

// setupTokenSaver keeps a setup token as an account folder's sign-in.
type setupTokenSaver interface {
	SaveSetupToken(home, token string) error
	SetupTokenOf(home string) (string, error)
}

// saveSetupToken saves token as a new setup token account, or as account's new
// token. The CLI must report the folder signed in with it before it is kept; a
// refused replacement puts the earlier token back.
func (s *Service) saveSetupToken(ctx context.Context, p domain.AgentAccountProvider, account *domain.AgentAccount, req LoginRequest) (LoginStart, error) {
	provider := s.providers[p]
	saver, ok := provider.(setupTokenSaver)
	if !ok {
		return LoginStart{}, ports.ErrAgentAccountMethodInvalid
	}
	signedIn := func(home string) error {
		identity, err := provider.Identity(ctx, home)
		if err != nil {
			return fmt.Errorf("%w: %w", ports.ErrAgentAccountKeyUnchecked, err)
		}
		if !identity.SignedIn {
			return ports.ErrAgentAccountKeyRejected
		}
		return nil
	}
	if account != nil {
		s.keyMu.Lock()
		defer s.keyMu.Unlock()
		if s.beingRemoved(account.ID) {
			return LoginStart{}, ports.ErrAgentAccountUnknown
		}
		previous, previousErr := saver.SetupTokenOf(account.Home)
		if err := saver.SaveSetupToken(account.Home, req.Secret); err != nil {
			return LoginStart{}, err
		}
		if err := signedIn(account.Home); err != nil {
			if previousErr == nil {
				if restoreErr := saver.SaveSetupToken(account.Home, previous); restoreErr != nil {
					s.log.Warn("restore setup token after a refused replace", "account", account.ID, "error", restoreErr)
				}
			}
			return LoginStart{}, err
		}
		updated := *account
		updated.KeyHint = apiKeyHint(req.Secret)
		if err := s.store.UpdateAgentAccountKeyHint(ctx, account.ID, updated.KeyHint); err != nil {
			return LoginStart{}, err
		}
		return s.finishSetupToken(ctx, updated)
	}
	id := uuid.NewString()
	home := filepath.Join(s.root, string(p), id)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return LoginStart{}, fmt.Errorf("create account folder: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			s.removeFolder(home)
		}
	}()
	if err := s.prepare(provider, home); err != nil {
		return LoginStart{}, err
	}
	if err := saver.SaveSetupToken(home, req.Secret); err != nil {
		return LoginStart{}, err
	}
	if err := signedIn(home); err != nil {
		return LoginStart{}, err
	}
	created := domain.AgentAccount{
		ID: id, Provider: p, Home: home, Kind: domain.AgentAccountKindSetupToken,
		Name: cleanAPIKeyName(req.Name), KeyHint: apiKeyHint(req.Secret), CreatedAt: s.now(),
	}
	if err := s.createColored(ctx, &created, ""); err != nil {
		return LoginStart{}, err
	}
	keep = true
	return s.finishSetupToken(ctx, created)
}

func (s *Service) finishSetupToken(ctx context.Context, account domain.AgentAccount) (LoginStart, error) {
	s.read(ctx, account)
	s.notifyDefaultChanged(account.Provider)
	view, err := s.view(ctx, account.ID)
	if err != nil {
		return LoginStart{}, err
	}
	return LoginStart{Provider: account.Provider, Account: &view}, nil
}
