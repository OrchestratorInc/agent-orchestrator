package accountsmanager

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

var (
	// ErrAccountNotFound indicates that a Codex account reference is unknown.
	ErrAccountNotFound = errors.New("codex account not found")
	// ErrAccountConflict indicates that a manager operation cannot be applied.
	ErrAccountConflict = errors.New("codex account operation conflicts with existing state")
	// ErrAccountInvalid indicates malformed or unsupported Codex credentials.
	ErrAccountInvalid = errors.New("codex account credential is invalid")
	// ErrAccountNotManaged prevents destructive manager operations on native accounts.
	ErrAccountNotManaged = errors.New("codex account is managed by AO's native account store")
)

// ManagementSnapshot is the compact, redacted management projection. It is
// intentionally backed by the same embedded SDK state used for routing.
type ManagementSnapshot struct {
	Revision     int64         `json:"revision"`
	Availability string        `json:"availability"`
	Stale        bool          `json:"stale"`
	Accounts     []Account     `json:"accounts"`
	Routing      RoutingPolicy `json:"routing"`
}

// RoutingPolicy describes the ordered Codex accounts used for new sessions.
type RoutingPolicy struct {
	Enabled    bool     `json:"enabled"`
	AccountIDs []string `json:"accountIds"`
}

// APIKeyInput contains a Codex API key and optional display/routing metadata.
type APIKeyInput struct {
	Key     string
	Label   string
	BaseURL string
}

// CredentialImport contains a Codex credential file payload.
type CredentialImport struct {
	Filename string
	JSON     json.RawMessage
}

// AccountQuota is the display-safe quota state for one Codex account.
type AccountQuota struct {
	Exceeded      bool              `json:"exceeded"`
	Reason        string            `json:"reason,omitempty"`
	NextRecoverAt time.Time         `json:"nextRecoverAt,omitempty"`
	ObservedAt    time.Time         `json:"observedAt,omitempty"`
	Signals       map[string]string `json:"signals,omitempty"`
}

// AccountModel identifies a model observed for one Codex account.
type AccountModel struct {
	ID string `json:"id"`
}

// Status reports whether the embedded proxy is ready to serve management calls.
func (s *Service) Status() (state, reason string) {
	if s == nil {
		return "degraded", "unavailable"
	}
	s.lifecycleMu.Lock()
	running := s.runDone != nil && !s.closed
	s.lifecycleMu.Unlock()
	if running {
		return "ready", ""
	}
	return "starting", "not_started"
}

// ManagementSnapshot returns a redacted Codex account and routing projection.
func (s *Service) ManagementSnapshot(ctx context.Context) (ManagementSnapshot, error) {
	if err := s.refreshAccounts(ctx); err != nil {
		return ManagementSnapshot{Availability: "degraded", Stale: true}, err
	}
	accounts, err := s.Accounts(ctx)
	if err != nil {
		return ManagementSnapshot{Availability: "degraded", Stale: true}, err
	}
	enabled, preferred := s.routes.routing()
	s.managementMu.Lock()
	now := time.Now().UnixMilli()
	if now <= s.revision {
		now = s.revision + 1
	}
	s.revision = now
	s.managementMu.Unlock()
	return ManagementSnapshot{
		Revision: now, Availability: "ready", Accounts: accounts,
		Routing: RoutingPolicy{Enabled: enabled, AccountIDs: preferred},
	}, nil
}

// AddAPIKey persists and loads a managed Codex API key.
func (s *Service) AddAPIKey(ctx context.Context, input APIKeyInput) (ManagementSnapshot, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	key := strings.TrimSpace(input.Key)
	if key == "" || len(key) > 8192 {
		return ManagementSnapshot{}, ErrAccountInvalid
	}
	baseURL, err := normalizeCodexBaseURL(input.BaseURL)
	if err != nil {
		return ManagementSnapshot{}, err
	}
	credential := map[string]string{"type": "codex", "access_token": key, "auth_mode": "api_key"}
	if label := strings.TrimSpace(input.Label); label != "" {
		credential["label"] = label
	}
	if baseURL != "" {
		credential["proxy_url"] = baseURL
	}
	raw, err := json.Marshal(credential)
	if err != nil {
		return ManagementSnapshot{}, fmt.Errorf("encode Codex API key: %w", err)
	}
	if err := s.writeManagedCredential(raw); err != nil {
		return ManagementSnapshot{}, err
	}
	return s.snapshotAndPublish(ctx)
}

func normalizeCodexBaseURL(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", ErrAccountInvalid
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	if parsed.Scheme != "https" {
		ip := net.ParseIP(parsed.Hostname())
		loopback := strings.EqualFold(parsed.Hostname(), "localhost") || (ip != nil && ip.IsLoopback())
		if parsed.Scheme != "http" || !loopback {
			return "", ErrAccountInvalid
		}
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/")
	return parsed.String(), nil
}

// ImportCredential persists and loads a redacted Codex credential file.
func (s *Service) ImportCredential(ctx context.Context, input CredentialImport) (ManagementSnapshot, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if len(input.JSON) == 0 || len(input.JSON) > 1<<20 {
		return ManagementSnapshot{}, ErrAccountInvalid
	}
	raw, ok := proxyCodexCredential(input.JSON)
	if !ok {
		return ManagementSnapshot{}, ErrAccountInvalid
	}
	return s.addManagedCredential(ctx, raw)
}

func (s *Service) addManagedCredential(ctx context.Context, raw []byte) (ManagementSnapshot, error) {
	if err := s.writeManagedCredential(raw); err != nil {
		return ManagementSnapshot{}, err
	}
	return s.snapshotAndPublish(ctx)
}

func (s *Service) snapshotAndPublish(ctx context.Context) (ManagementSnapshot, error) {
	snapshot, err := s.ManagementSnapshot(ctx)
	if err == nil {
		s.publishManagement(snapshot)
	}
	return snapshot, err
}

func (s *Service) writeManagedCredential(raw []byte) error {
	if s == nil || s.authDir == "" {
		return ports.ErrCodexProxyUnavailable
	}
	nameBytes, err := randomBytes(12)
	if err != nil {
		return err
	}
	name := "ao-managed-" + hex.EncodeToString(nameBytes) + ".json"
	return writePrivateFile(filepath.Join(s.authDir, name), raw)
}

// SetAccountDisabled enables or disables one Codex account.
func (s *Service) SetAccountDisabled(ctx context.Context, id string, disabled bool) (ManagementSnapshot, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	auth, err := s.managementAuth(ctx, id)
	if err != nil {
		return ManagementSnapshot{}, err
	}
	auth.Disabled, auth.Status = disabled, coreauth.StatusActive
	if disabled {
		auth.Status = coreauth.StatusDisabled
	}
	if _, err = s.coreManager.Update(ctx, auth); err != nil {
		return ManagementSnapshot{}, err
	}
	return s.snapshotAndPublish(ctx)
}

// RefreshAccount refreshes OAuth credentials or reloads an API-key file.
func (s *Service) RefreshAccount(ctx context.Context, id string) (ManagementSnapshot, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	auth, err := s.managementAuth(ctx, id)
	if err != nil {
		return ManagementSnapshot{}, err
	}
	if authKind(auth) == "oauth" {
		if _, err = s.coreManager.ForceRefreshAuth(ctx, auth.ID); err != nil {
			return ManagementSnapshot{}, err
		}
	} else if err := s.coreManager.Load(ctx); err != nil {
		return ManagementSnapshot{}, err
	}
	return s.snapshotAndPublish(ctx)
}

// RemoveAccount deletes a manager-owned Codex credential.
func (s *Service) RemoveAccount(ctx context.Context, id string) (ManagementSnapshot, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	auth, err := s.managementAuth(ctx, id)
	if err != nil {
		return ManagementSnapshot{}, err
	}
	s.nativeMu.Lock()
	for _, nativeID := range s.nativeRefs {
		if nativeID == auth.ID {
			s.nativeMu.Unlock()
			return ManagementSnapshot{}, ErrAccountNotManaged
		}
	}
	s.nativeMu.Unlock()
	s.coreManager.Remove(ctx, auth.ID)
	if path := authPath(auth, s.authDir); path != "" {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return ManagementSnapshot{}, err
		}
	}
	return s.snapshotAndPublish(ctx)
}

// Models returns models observed for a Codex account.
func (s *Service) Models(ctx context.Context, id string) ([]AccountModel, error) {
	auth, err := s.managementAuth(ctx, id)
	if err != nil {
		return nil, err
	}
	models := make([]AccountModel, 0, len(auth.ModelStates))
	for model := range auth.ModelStates {
		if strings.TrimSpace(model) != "" {
			models = append(models, AccountModel{ID: model})
		}
	}
	if len(models) == 0 {
		models = append(models, AccountModel{ID: "gpt-5-codex"})
	}
	sort.Slice(models, func(i, j int) bool { return models[i].ID < models[j].ID })
	return models, nil
}

// Quota returns the cached quota projection for a Codex account.
func (s *Service) Quota(ctx context.Context, id string) (AccountQuota, error) {
	auth, err := s.managementAuth(ctx, id)
	if err != nil {
		return AccountQuota{}, err
	}
	return AccountQuota{Exceeded: auth.Quota.Exceeded, Reason: auth.Quota.Reason,
		NextRecoverAt: auth.Quota.NextRecoverAt, ObservedAt: auth.Quota.ObservedAt,
		Signals: cloneStringMap(auth.Quota.Signals)}, nil
}

// ResetQuota clears cached quota/cooldown state for a Codex account.
func (s *Service) ResetQuota(ctx context.Context, id string) error {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	auth, err := s.managementAuth(ctx, id)
	if err != nil {
		return err
	}
	auth.Quota = coreauth.QuotaState{}
	if _, err = s.coreManager.Update(ctx, auth); err != nil {
		return err
	}
	_, err = s.snapshotAndPublish(ctx)
	return err
}

// SetRoutingPolicy persists the ordered Codex accounts used for new sessions.
func (s *Service) SetRoutingPolicy(ctx context.Context, enabled bool, accountIDs []string) (ManagementSnapshot, error) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.refreshAccounts(ctx); err != nil {
		return ManagementSnapshot{}, err
	}
	ids := compactStrings(accountIDs)
	if enabled && len(ids) == 0 {
		return ManagementSnapshot{}, ErrAccountConflict
	}
	eligible := false
	for _, id := range ids {
		auth, err := s.managementAuth(ctx, id)
		if err != nil || !strings.EqualFold(auth.Provider, "codex") {
			return ManagementSnapshot{}, ErrAccountNotFound
		}
		if exactRouteAuthUsable(auth, "", time.Now()) {
			eligible = true
		}
	}
	if enabled && !eligible {
		return ManagementSnapshot{}, ErrAccountConflict
	}
	if err := s.routes.setRouting(enabled, ids); err != nil {
		return ManagementSnapshot{}, err
	}
	return s.snapshotAndPublish(ctx)
}

func (s *Service) managementAuth(ctx context.Context, id string) (*coreauth.Auth, error) {
	if err := s.refreshAccounts(ctx); err != nil {
		return nil, err
	}
	id = strings.TrimSpace(id)
	s.nativeMu.Lock()
	proxyID := s.nativeRefs[id]
	s.nativeMu.Unlock()
	if proxyID != "" {
		id = proxyID
	}
	auth, ok := s.coreManager.GetByID(id)
	if !ok || auth == nil || !strings.EqualFold(auth.Provider, "codex") {
		return nil, ErrAccountNotFound
	}
	return auth, nil
}

func authPath(auth *coreauth.Auth, root string) string {
	if auth == nil {
		return ""
	}
	path := ""
	if auth.Attributes != nil && strings.TrimSpace(auth.Attributes["path"]) != "" {
		path = strings.TrimSpace(auth.Attributes["path"])
	} else if safePathComponent(auth.FileName) {
		path = filepath.Join(root, auth.FileName)
	}
	if path == "" || strings.TrimSpace(root) == "" {
		return ""
	}
	rootAbs, rootErr := filepath.Abs(root)
	pathAbs, pathErr := filepath.Abs(path)
	if rootErr != nil || pathErr != nil {
		return ""
	}
	rel, relErr := filepath.Rel(rootAbs, pathAbs)
	if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return ""
	}
	return pathAbs
}

func authKind(auth *coreauth.Auth) string {
	if auth == nil {
		return "unknown"
	}
	if auth.Metadata != nil {
		if value, ok := auth.Metadata["auth_mode"].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
		if _, ok := auth.Metadata["OPENAI_API_KEY"]; ok {
			return "api_key"
		}
	}
	if strings.HasPrefix(auth.ID, "ao-managed-") {
		return "api_key"
	}
	return "oauth"
}

func authModels(auth *coreauth.Auth) []string {
	models := make([]string, 0, len(auth.ModelStates))
	for model := range auth.ModelStates {
		models = append(models, model)
	}
	sort.Strings(models)
	return models
}

func authCooldowns(auth *coreauth.Auth) []AccountCooldown {
	if auth == nil {
		return nil
	}
	if !auth.NextRetryAfter.IsZero() {
		return []AccountCooldown{{Reason: auth.Quota.Reason, RetryAt: auth.NextRetryAfter, Model: ""}}
	}
	return nil
}
