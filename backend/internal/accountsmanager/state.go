package accountsmanager

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

type persistedRouteState struct {
	ActiveAccount     string            `json:"active_account,omitempty"`
	Sessions          map[string]string `json:"sessions"`
	RoutingEnabled    *bool             `json:"routing_enabled,omitempty"`
	PreferredAccounts []string          `json:"preferred_accounts,omitempty"`
}

// routeState is the small durable part of Accounts Manager state. It stores
// only opaque account references and AO session ids, never tokens or account
// credentials.
type routeState struct {
	path string

	mu                sync.RWMutex
	activeAccount     string
	sessions          map[string]string
	routingSet        bool
	routingEnabled    bool
	preferredAccounts []string
}

func newRouteState(path string) (*routeState, error) {
	state := &routeState{path: path, sessions: make(map[string]string)}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return state, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read accounts manager routes: %w", err)
	}
	if err := os.Chmod(path, 0o600); err != nil {
		return nil, fmt.Errorf("protect accounts manager routes: %w", err)
	}
	var persisted persistedRouteState
	if err := json.Unmarshal(raw, &persisted); err != nil {
		return nil, fmt.Errorf("decode accounts manager routes: %w", err)
	}
	for sessionID, accountID := range persisted.Sessions {
		sessionID = strings.TrimSpace(sessionID)
		accountID = strings.TrimSpace(accountID)
		if sessionID != "" && accountID != "" {
			state.sessions[sessionID] = accountID
		}
	}
	state.activeAccount = strings.TrimSpace(persisted.ActiveAccount)
	if persisted.RoutingEnabled != nil {
		state.routingSet = true
		state.routingEnabled = *persisted.RoutingEnabled
		state.preferredAccounts = compactStrings(persisted.PreferredAccounts)
	}
	return state, nil
}

func (s *routeState) accountForSession(sessionID string) (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	accountID, ok := s.sessions[strings.TrimSpace(sessionID)]
	s.mu.RUnlock()
	return accountID, ok
}

func (s *routeState) activeAccountID() (string, bool) {
	if s == nil {
		return "", false
	}
	s.mu.RLock()
	accountID := s.activeAccount
	s.mu.RUnlock()
	return accountID, accountID != ""
}

func (s *routeState) setAccountForSession(sessionID, accountID string) error {
	if s == nil {
		return ports.ErrCodexProxyUnavailable
	}
	sessionID = strings.TrimSpace(sessionID)
	accountID = strings.TrimSpace(accountID)
	if sessionID == "" || accountID == "" {
		return errors.New("session and account ids are required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := cloneStringMap(s.sessions)
	candidate[sessionID] = accountID
	if err := s.persist(s.persisted(candidate)); err != nil {
		return err
	}
	s.sessions = candidate
	return nil
}

// setAccountForAllSessions records the selected account as the default for
// sessions that have not been routed yet. Existing session pins are retained
// so one session's account switch cannot change another session's account.
func (s *routeState) setAccountForAllSessions(accountID string) error {
	if s == nil {
		return ports.ErrCodexProxyUnavailable
	}
	accountID = strings.TrimSpace(accountID)
	if accountID == "" {
		return errors.New("account id is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := cloneStringMap(s.sessions)
	previous := s.activeAccount
	s.activeAccount = accountID
	if err := s.persist(s.persisted(candidate)); err != nil {
		s.activeAccount = previous
		return err
	}
	s.sessions = candidate
	return nil
}

func (s *routeState) routing() (bool, []string) {
	if s == nil {
		return true, nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.routingEnabled || !s.routingSet, append([]string(nil), s.preferredAccounts...)
}

func (s *routeState) setRouting(enabled bool, accountIDs []string) error {
	if s == nil {
		return ports.ErrCodexProxyUnavailable
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	candidate := compactStrings(accountIDs)
	enabledCopy := enabled
	if err := s.persist(persistedRouteState{
		ActiveAccount: s.activeAccount, Sessions: cloneStringMap(s.sessions),
		RoutingEnabled: &enabledCopy, PreferredAccounts: candidate,
	}); err != nil {
		return err
	}
	s.routingSet, s.routingEnabled, s.preferredAccounts = true, enabled, candidate
	return nil
}

func (s *routeState) persisted(sessions map[string]string) persistedRouteState {
	state := persistedRouteState{ActiveAccount: s.activeAccount, Sessions: sessions}
	if s.routingSet {
		state.RoutingEnabled = &s.routingEnabled
		state.PreferredAccounts = append([]string(nil), s.preferredAccounts...)
	}
	return state
}

func (s *routeState) persist(persisted persistedRouteState) error {
	raw, err := json.MarshalIndent(persisted, "", "  ")
	if err != nil {
		return fmt.Errorf("encode accounts manager routes: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("create accounts manager route directory: %w", err)
	}
	tmp, err := os.CreateTemp(filepath.Dir(s.path), ".routes-*.tmp")
	if err != nil {
		return fmt.Errorf("stage accounts manager routes: %w", err)
	}
	tmpName := tmp.Name()
	defer func() {
		_ = os.Remove(tmpName)
	}()
	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("protect accounts manager routes: %w", err)
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write accounts manager routes: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close accounts manager routes: %w", err)
	}
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("commit accounts manager routes: %w", err)
	}
	return nil
}

func cloneStringMap(input map[string]string) map[string]string {
	if len(input) == 0 {
		return map[string]string{}
	}
	result := make(map[string]string, len(input))
	for key, value := range input {
		result[key] = value
	}
	return result
}

func compactStrings(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, value := range input {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		result = append(result, value)
	}
	return result
}
