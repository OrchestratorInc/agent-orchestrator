package agentauth

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

var unpublishProviderKey = providerkeys.Unpublish

var companionVariables = map[string][]string{"LLM_API_KEY": {"LLM_MODEL"}}

type ProviderKey struct {
	Name            string   `json:"name"`
	Label           string   `json:"label"`
	Configured      bool     `json:"configured"`
	Hint            string   `json:"hint,omitempty"`
	Device          bool     `json:"device"`
	DeviceAvailable bool     `json:"deviceAvailable"`
	Checkable       bool     `json:"checkable"`
	Agents          []string `json:"agents"`
	WithdrawFailed  bool     `json:"withdrawFailed,omitempty"`
}

type ProviderKeyCheck = providerkeys.CheckResult

type providerKeyInfo struct {
	label  string
	agents []string
}

var providerKeyCatalog = func() map[string]providerKeyInfo {
	out := map[string]providerKeyInfo{}
	for _, plan := range plans {
		for _, method := range providerEnvironment[plan.AgentID] {
			name := strings.TrimPrefix(method.ID, "env_")
			info, ok := out[name]
			if !ok {
				info.label = method.Label
			}
			info.agents = append(info.agents, plan.AgentID)
			out[name] = info
		}
	}
	return out
}()

func keyHint(value string) string {
	if len(value) < 16 {
		return ""
	}
	return value[len(value)-4:]
}

func (s *Service) ProviderKeys(context.Context) ([]ProviderKey, error) {
	keys, err := providerkeys.Load(s.dataDir)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	device, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	out := make([]ProviderKey, 0, len(providerKeyCatalog))
	for name, info := range providerKeyCatalog {
		value, configured := keys[name]
		out = append(out, ProviderKey{
			Name: name, Label: info.label, Configured: configured, Hint: keyHint(value),
			Device: configured && device[name], DeviceAvailable: deviceSupported, Checkable: providerkeys.Checkable(name),
			Agents: append([]string(nil), info.agents...),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Agents) != len(out[j].Agents) {
			return len(out[i].Agents) > len(out[j].Agents)
		}
		return out[i].Label < out[j].Label
	})
	return out, nil
}

func (s *Service) providerKey(ctx context.Context, name string) (ProviderKey, error) {
	keys, err := s.ProviderKeys(ctx)
	if err != nil {
		return ProviderKey{}, err
	}
	for _, key := range keys {
		if key.Name == name {
			return key, nil
		}
	}
	return ProviderKey{}, unknownProviderKey(name)
}

func unknownProviderKey(name string) error {
	return apierr.Invalid("PROVIDER_KEY_UNKNOWN", "Choose a supported provider key.", map[string]any{"name": name})
}

func (s *Service) SaveProviderKey(ctx context.Context, name, secret string, device bool) (ProviderKey, error) {
	if _, ok := providerKeyCatalog[name]; !ok {
		return ProviderKey{}, unknownProviderKey(name)
	}
	secret = strings.TrimSpace(secret)
	if len(secret) > 16*1024 || strings.ContainsAny(secret, "\r\n\x00") {
		return ProviderKey{}, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Enter the key on a single line.", nil)
	}
	withdrawFailed, err := s.storeProviderKeyReporting(ctx, name, secret, &device, "")
	if err != nil {
		return ProviderKey{}, err
	}
	key, err := s.providerKey(ctx, name)
	if err != nil {
		return ProviderKey{}, err
	}
	key.WithdrawFailed = withdrawFailed
	return key, nil
}

func (s *Service) storeProviderKeyReporting(ctx context.Context, name, secret string, share *bool, harness string) (bool, error) {
	s.keysMu.Lock()
	defer s.keysMu.Unlock()
	if share != nil && *share && !deviceSupported {
		return false, apierr.Invalid("AGENT_AUTH_DEVICE_ENV_UNSUPPORTED", "Sharing a key with other apps isn't available on this device.", nil)
	}
	keys, err := providerkeys.Load(s.dataDir)
	if err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	device := share != nil && *share
	if share == nil {
		current, err := providerkeys.DeviceNames(s.dataDir)
		if err != nil {
			return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
		}
		device = current[name] && keys[name] != "" && deviceSupported
	}
	if secret == "" {
		secret = keys[name]
		if secret == "" {
			return false, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Enter a key.", nil)
		}
	}
	names := append([]string{name}, companionVariables[name]...)
	values := map[string]string{name: secret}
	for _, companion := range companionVariables[name] {
		if value := keys[companion]; value != "" {
			values[companion] = value
		}
	}
	present := make([]string, 0, len(names))
	for _, n := range names {
		if _, ok := values[n]; ok {
			present = append(present, n)
		}
	}
	undoPublish := func() {}
	withdrawFailed := false
	if device {
		if undoPublish, err = s.publishProviderKeys(ctx, present, values, keys); err != nil {
			return false, err
		}
	} else {
		for _, n := range present {
			failed, err := s.withdrawProviderKey(ctx, n, keys[n])
			if err != nil {
				return false, err
			}
			withdrawFailed = withdrawFailed || failed
		}
	}
	if err := providerkeys.Save(ctx, s.dataDir, name, secret); err != nil {
		undoPublish()
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be saved in AO's device credential store.")
	}
	if !device {
		for _, n := range present {
			if err := providerkeys.SetDevice(ctx, s.dataDir, n, false); err != nil {
				return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The device setting could not be saved.")
			}
		}
	}
	if harness != "" {
		if err := providerkeys.ClaimForHarness(ctx, s.dataDir, name, harness); err != nil {
			return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be saved for this harness.")
		}
	}
	if err := providerkeys.ExportForProbes(name, secret); err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be activated.")
	}
	return withdrawFailed, nil
}

func (s *Service) releaseSubscriptionKey(ctx context.Context, agentID string) (string, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		return "", nil
	}
	return providerkeys.ReleaseHarness(ctx, s.dataDir, agentID)
}

func (s *Service) restoreSubscriptionKey(ctx context.Context, agentID, released string) error {
	if released == "" {
		return nil
	}
	return providerkeys.ClaimForHarness(ctx, s.dataDir, released, agentID)
}

func (s *Service) publishProviderKeys(ctx context.Context, names []string, values, stored map[string]string) (undo func(), err error) {
	before, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	users, err := providerkeys.UserValueNames(s.dataDir)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	var fresh, usersHere, reclaimed []string
	replaced := map[string]string{}
	undo = func() {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		s.rollbackPublished(cleanupCtx, fresh, values)
		for _, n := range usersHere {
			_ = providerkeys.SetDevice(cleanupCtx, s.dataDir, n, false)
		}
		for _, n := range reclaimed {
			_ = unpublishProviderKey(cleanupCtx, n, values[n])
			_ = providerkeys.SetUserValue(cleanupCtx, s.dataDir, n, true)
		}
		for n, earlier := range replaced {
			_ = publishProviderKey(cleanupCtx, n, earlier, values[n])
		}
	}
	for _, n := range names {
		previous := ""
		if before[n] && !users[n] {
			previous = stored[n]
		}
		if err := providerkeys.SetDevice(ctx, s.dataDir, n, true); err != nil {
			undo()
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The device setting could not be saved.")
		}
		err := publishProviderKey(ctx, n, values[n], previous)
		if errors.Is(err, providerkeys.ErrAlreadySet) {
			if markErr := providerkeys.SetUserValue(ctx, s.dataDir, n, true); markErr != nil {
				if !before[n] {
					clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
					_ = providerkeys.SetDevice(clearCtx, s.dataDir, n, false)
					cancel()
				}
				undo()
				return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The device setting could not be saved.")
			}
			if !before[n] {
				usersHere = append(usersHere, n)
			}
			continue
		}
		if err != nil {
			if !before[n] {
				clearCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
				_ = providerkeys.SetDevice(clearCtx, s.dataDir, n, false)
				cancel()
			}
			undo()
			if errors.Is(err, providerkeys.ErrUserValue) {
				return nil, apierr.Invalid("AGENT_AUTH_DEVICE_ENV_TAKEN", "Your user environment already sets "+n+" to a different value. AO left it unchanged; remove it there first, or save the key without sharing it.", map[string]any{"name": n})
			}
			return nil, apierr.Unavailable("AGENT_AUTH_DEVICE_ENV_UNAVAILABLE", "The key could not be added to this device's user environment.")
		}
		if users[n] {
			reclaimed = append(reclaimed, n)
			if err := providerkeys.SetUserValue(ctx, s.dataDir, n, false); err != nil {
				undo()
				return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The device setting could not be saved.")
			}
		}
		if !before[n] {
			fresh = append(fresh, n)
		} else if previous != "" && previous != values[n] {
			replaced[n] = previous
		}
	}
	return undo, nil
}

func (s *Service) rollbackPublished(ctx context.Context, names []string, values map[string]string) {
	for _, n := range names {
		_ = unpublishProviderKey(ctx, n, values[n])
		_ = providerkeys.SetDevice(ctx, s.dataDir, n, false)
	}
}

func (s *Service) withdrawProviderKey(ctx context.Context, name, value string) (failed bool, err error) {
	device, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	users, err := providerkeys.UserValueNames(s.dataDir)
	if err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	if device[name] && !users[name] {
		failed = unpublishProviderKey(ctx, name, value) != nil
	}
	return failed, nil
}

func (s *Service) RemoveProviderKey(ctx context.Context, name string) (ProviderKey, error) {
	if _, ok := providerKeyCatalog[name]; !ok {
		return ProviderKey{}, unknownProviderKey(name)
	}
	withdrawFailed, err := s.removeProviderKey(ctx, name)
	if err != nil {
		return ProviderKey{}, err
	}
	key, err := s.providerKey(ctx, name)
	if err != nil {
		return ProviderKey{}, err
	}
	key.WithdrawFailed = withdrawFailed
	return key, nil
}

func (s *Service) removeProviderKey(ctx context.Context, name string) (bool, error) {
	s.keysMu.Lock()
	defer s.keysMu.Unlock()
	keys, err := providerkeys.Load(s.dataDir)
	if err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	withdrawFailed := false
	for _, n := range append([]string{name}, companionVariables[name]...) {
		failed, err := s.withdrawProviderKey(ctx, n, keys[n])
		if err != nil {
			return false, err
		}
		withdrawFailed = withdrawFailed || failed
		if err := providerkeys.Remove(ctx, s.dataDir, n); err != nil {
			return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be removed.")
		}
		_ = providerkeys.RestoreInherited(n)
	}
	return withdrawFailed, nil
}

func (s *Service) CheckProviderKey(ctx context.Context, name, secret string) (ProviderKeyCheck, error) {
	if _, ok := providerKeyCatalog[name]; !ok {
		return ProviderKeyCheck{}, unknownProviderKey(name)
	}
	secret = strings.TrimSpace(secret)
	if secret == "" {
		keys, err := providerkeys.Load(s.dataDir)
		if err != nil {
			return ProviderKeyCheck{}, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
		}
		secret = keys[name]
		if secret == "" {
			return ProviderKeyCheck{}, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Enter a key to check.", nil)
		}
	}
	result, err := providerkeys.Check(ctx, name, secret)
	if err != nil {
		return ProviderKeyCheck{}, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Enter the key on a single line.", nil)
	}
	return result, nil
}
