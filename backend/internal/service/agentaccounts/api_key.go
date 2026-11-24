package agentaccounts

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/google/uuid"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/domain"
	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/ports"
)

// An API key account is an account folder whose CLI runs on a saved API key,
// billed per token, instead of a subscription sign-in. Its folder links to the
// computer's history like any other, so a session can move to and from it by
// hand; it has no usage limits, so none is read and no session moves to or from
// it at a limit.

// apiKeyFile holds an API key account's key inside its own folder, readable by
// the user only. The key is never stored in the database or sent to the app.
const apiKeyFile = ".ao-api-key" //nolint:gosec // a file name, not a credential

// apiKeyEnv is the variable each provider's CLI reads an API key from. A
// provider without one has no API key accounts.
var apiKeyEnv = map[domain.AgentAccountProvider]string{domain.AgentAccountProviderClaude: "ANTHROPIC_API_KEY"}

// maxAPIKeyName bounds the label the user gives an API key account.
const maxAPIKeyName = 80

// apiKeyApprover records in an account folder that the user accepted a key, so
// the CLI does not stop to ask about it in a session.
type apiKeyApprover interface {
	ApproveAPIKey(home, key string) error
}

// KeyCheck asks the provider whether it accepts key. It returns
// ports.ErrAgentAccountKeyRejected for a key the provider refused, and any other
// error when the provider could not answer.
type KeyCheck func(ctx context.Context, p domain.AgentAccountProvider, key string) error

// SetKeyCheck late-binds the check an API key passes before it is saved. Without
// one, keys are saved unchecked.
func (s *Service) SetKeyCheck(check KeyCheck) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.keyCheck = check
}

func (s *Service) checkKey(ctx context.Context, p domain.AgentAccountProvider, key string) error {
	s.mu.Lock()
	check := s.keyCheck
	s.mu.Unlock()
	if check == nil {
		return nil
	}
	err := check(ctx, p, key)
	switch {
	case err == nil, errors.Is(err, ports.ErrAgentAccountKeyRejected):
		return err
	default:
		return fmt.Errorf("%w: %w", ports.ErrAgentAccountKeyUnchecked, err)
	}
}

// AddAPIKeyAccount saves key as a new account of p, named name, once the
// provider accepts it.
func (s *Service) AddAPIKeyAccount(ctx context.Context, p domain.AgentAccountProvider, name, key string) (AccountView, error) {
	provider := s.providers[p]
	approver, ok := provider.(apiKeyApprover)
	if provider == nil || !ok || apiKeyEnv[p] == "" {
		return AccountView{}, ports.ErrAgentAccountProviderMismatch
	}
	key, err := cleanAPIKey(key)
	if err != nil {
		return AccountView{}, err
	}
	if err := s.checkKey(ctx, p, key); err != nil {
		return AccountView{}, err
	}
	id := uuid.NewString()
	home := filepath.Join(s.root, string(p), id)
	if err := os.MkdirAll(home, 0o700); err != nil {
		return AccountView{}, fmt.Errorf("create account folder: %w", err)
	}
	keep := false
	defer func() {
		if !keep {
			s.removeFolder(home)
		}
	}()
	// Linking first means the CLI never creates a private copy of a shared folder.
	if err := s.prepare(provider, home); err != nil {
		return AccountView{}, err
	}
	if err := writeAPIKey(home, key); err != nil {
		return AccountView{}, err
	}
	if err := approver.ApproveAPIKey(home, key); err != nil {
		return AccountView{}, fmt.Errorf("approve api key: %w", err)
	}
	account := domain.AgentAccount{
		ID: id, Provider: p, Home: home, Kind: domain.AgentAccountKindAPIKey,
		Name: cleanAPIKeyName(name), KeyHint: apiKeyHint(key), CreatedAt: s.now(),
	}
	if err := s.store.CreateAgentAccount(ctx, account); err != nil {
		return AccountView{}, err
	}
	keep = true
	s.read(ctx, account)
	s.notifyDefaultChanged(p)
	return s.view(ctx, id)
}

// ReplaceAPIKey saves key in place of an API key account's key, once the
// provider accepts it. A replacement that fails keeps the account's current key
// and key hint. Agents already running keep the key they started with until
// they start again.
func (s *Service) ReplaceAPIKey(ctx context.Context, accountID, key string) (AccountView, error) {
	key, err := cleanAPIKey(key)
	if err != nil {
		return AccountView{}, err
	}
	account, err := s.find(ctx, accountID)
	if err != nil {
		return AccountView{}, err
	}
	if !account.APIKey() {
		return AccountView{}, ports.ErrAgentAccountKindMismatch
	}
	approver, ok := s.providers[account.Provider].(apiKeyApprover)
	if !ok {
		return AccountView{}, ports.ErrAgentAccountProviderMismatch
	}
	if err := s.checkKey(ctx, account.Provider, key); err != nil {
		return AccountView{}, err
	}
	// One key change at a time, and none once a removal has begun, so the key
	// file, its approval and its hint always describe the same key.
	s.keyMu.Lock()
	defer s.keyMu.Unlock()
	if s.beingRemoved(account.ID) {
		return AccountView{}, ports.ErrAgentAccountUnknown
	}
	if account, err = s.find(ctx, accountID); err != nil {
		return AccountView{}, err
	}
	previous, previousErr := readAPIKey(account.Home)
	// Approving the new key first leaves the current one working if this fails.
	if err := approver.ApproveAPIKey(account.Home, key); err != nil {
		return AccountView{}, fmt.Errorf("approve api key: %w", err)
	}
	if err := writeAPIKey(account.Home, key); err != nil {
		return AccountView{}, err
	}
	account.KeyHint = apiKeyHint(key)
	if err := s.store.UpdateAgentAccountKeyHint(ctx, account.ID, account.KeyHint); err != nil {
		if previousErr == nil {
			if restoreErr := writeAPIKey(account.Home, previous); restoreErr != nil {
				s.log.Warn("restore api key after a failed replace", "account", account.ID, "error", restoreErr)
			}
		}
		return AccountView{}, err
	}
	s.read(ctx, account)
	s.notifyDefaultChanged(account.Provider)
	return s.view(ctx, account.ID)
}

// view is one account as List shows it.
func (s *Service) view(ctx context.Context, id string) (AccountView, error) {
	views, err := s.List(ctx, false)
	if err != nil {
		return AccountView{}, err
	}
	for _, view := range views {
		if view.Account.ID == id {
			return view, nil
		}
	}
	return AccountView{}, ports.ErrAgentAccountUnknown
}

// readAPIKeyAccount is read for an API key account: it is logged in while its
// key is saved. There is no sign-in to ask the CLI about and no limits to read.
func (s *Service) readAPIKeyAccount(ctx context.Context, account domain.AgentAccount) *reading {
	// The sequence is taken before the file is read, so a reading that saw an
	// older file never replaces one that saw a newer key saved meanwhile.
	s.mu.Lock()
	s.readSeq++
	seq := s.readSeq
	s.mu.Unlock()
	_, err := readAPIKey(account.Home)
	s.mu.Lock()
	defer s.mu.Unlock()
	r := &reading{at: s.now(), seq: seq, identity: domain.AgentAccountIdentityCheck{LoggedIn: err == nil}}
	confirmed := r.identity
	r.confirmed, r.confirmedSeq = &confirmed, r.seq
	if prev := s.readings[account.ID]; prev != nil && prev.seq > seq {
		return prev
	}
	if ctx.Err() == nil {
		s.readings[account.ID] = r
	}
	return r
}

// usable reports whether account can launch its CLI as itself now: a
// subscription account signed in as its own login, or a per-token account (an
// API key saved, or a metered CLI sign-in) that is logged in.
func usable(account domain.AgentAccount, r *reading) bool {
	// A folder signed in as another login, or to another organization, is not
	// this account, however it signs in.
	if r == nil || r.mismatch {
		return false
	}
	if account.PerToken() {
		return r.identity.LoggedIn
	}
	return r.identity.SignedIn
}

// accountLaunchEnv is the environment that runs provider's CLI as account.
func (s *Service) accountLaunchEnv(provider ports.AgentAccountProviderCLI, account domain.AgentAccount) (map[string]string, error) {
	env := provider.LaunchEnv(account.Home)
	if !account.APIKey() {
		return env, nil
	}
	key, err := readAPIKey(account.Home)
	if err != nil {
		return env, err
	}
	if env == nil {
		env = map[string]string{}
	}
	env[apiKeyEnv[account.Provider]] = key
	return env, nil
}

// cleanAPIKey trims key, which must be one token of printable characters.
func cleanAPIKey(key string) (string, error) {
	key = strings.TrimSpace(key)
	if key == "" || len(key) > 16*1024 || strings.IndexFunc(key, func(r rune) bool { return unicode.IsSpace(r) || !unicode.IsPrint(r) }) >= 0 {
		return "", ports.ErrAgentAccountKeyRejected
	}
	return key, nil
}

// cleanAPIKeyName is the label as shown: one line, at most maxAPIKeyName runes.
func cleanAPIKeyName(name string) string {
	name = strings.Join(strings.Fields(name), " ")
	if runes := []rune(name); len(runes) > maxAPIKeyName {
		name = strings.TrimSpace(string(runes[:maxAPIKeyName]))
	}
	return name
}

// apiKeyHint is the last four characters of key, which tell keys apart without
// saying anything that would let someone use one.
func apiKeyHint(key string) string {
	if len(key) <= 8 {
		return ""
	}
	return key[len(key)-4:]
}

func writeAPIKey(home, key string) error {
	path := filepath.Join(home, apiKeyFile)
	// CreateTemp makes the file readable by the user only (0600).
	tmp, err := os.CreateTemp(home, apiKeyFile+".*")
	if err != nil {
		return fmt.Errorf("save api key: %w", err)
	}
	// Gone after the rename; removed here only when saving failed.
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.WriteString(key); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("save api key: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("save api key: %w", err)
	}
	if err := os.Rename(tmp.Name(), path); err != nil {
		return fmt.Errorf("save api key: %w", err)
	}
	return nil
}

func readAPIKey(home string) (string, error) {
	if strings.TrimSpace(home) == "" {
		return "", errors.New("read api key: no account folder")
	}
	data, err := os.ReadFile(filepath.Join(home, apiKeyFile))
	if err != nil {
		return "", fmt.Errorf("read api key: %w", err)
	}
	key, err := cleanAPIKey(string(data))
	if err != nil {
		return "", errors.New("read api key: the saved key is invalid")
	}
	return key, nil
}
