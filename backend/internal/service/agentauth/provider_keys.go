package agentauth

import (
	"context"
	"os"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

var unpublishProviderKey = providerkeys.Unpublish

// companionVariables are saved with a key and follow its device setting and
// removal; they are never listed on their own.
var companionVariables = map[string][]string{"LLM_API_KEY": {"LLM_MODEL"}}

// ProviderKey is the display-safe state of one reviewed provider variable.
// The value never leaves the daemon; Hint is at most its last four characters.
type ProviderKey struct {
	Name       string `json:"name"`
	Label      string `json:"label"`
	Configured bool   `json:"configured"`
	Hint       string `json:"hint,omitempty"`
	// Device reports that the key is also in the OS user environment, for
	// programs started outside AO.
	Device bool `json:"device"`
	// Checkable reports that AO can ask the provider to accept the key.
	Checkable bool `json:"checkable"`
	// Agents lists the harnesses that read this variable.
	Agents []string `json:"agents"`
}

// ProviderKeyCheck is the provider's answer for one key.
type ProviderKeyCheck = providerkeys.CheckResult

type providerKeyInfo struct {
	label  string
	agents []string
}

// providerKeyCatalog is every reviewed environment method, keyed by variable.
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

// ProviderKeys lists every reviewed provider variable, those read by more
// harnesses first.
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
			Device: configured && device[name], Checkable: providerkeys.Checkable(name),
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

// SaveProviderKey stores secret for name, or keeps the stored value when
// secret is empty, and applies the device setting.
func (s *Service) SaveProviderKey(ctx context.Context, name, secret string, device bool) (ProviderKey, error) {
	if _, ok := providerKeyCatalog[name]; !ok {
		return ProviderKey{}, unknownProviderKey(name)
	}
	secret = strings.TrimSpace(secret)
	if len(secret) > 16*1024 || strings.ContainsAny(secret, "\r\n\x00") {
		return ProviderKey{}, apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Enter the key on a single line.", nil)
	}
	if err := s.storeProviderKey(ctx, name, secret, device, ""); err != nil {
		return ProviderKey{}, err
	}
	return s.providerKey(ctx, name)
}

// storeProviderKey saves name (and, when secret is empty, reuses the stored
// value), activates it for this daemon, and publishes or withdraws it from the
// OS user environment together with its companions. A non-empty harness is
// the harness whose own panel saved it, which lets that harness's sessions
// receive a key that would otherwise replace its subscription sign-in.
func (s *Service) storeProviderKey(ctx context.Context, name, secret string, device bool, harness string) error {
	keys, err := providerkeys.Load(s.dataDir)
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	if secret == "" {
		secret = keys[name]
		if secret == "" {
			return apierr.Invalid("AGENT_AUTH_SECRET_INVALID", "Enter a key.", nil)
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
	var fresh []string
	if device {
		if fresh, err = s.publishProviderKeys(ctx, present, values); err != nil {
			return err
		}
	} else {
		for _, n := range present {
			if err := s.withdrawProviderKey(ctx, n); err != nil {
				return err
			}
		}
	}
	if err := providerkeys.Save(ctx, s.dataDir, name, secret); err != nil {
		s.rollbackPublished(ctx, fresh)
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be saved in AO's device credential store.")
	}
	if !device {
		for _, n := range present {
			if err := providerkeys.SetDevice(ctx, s.dataDir, n, false); err != nil {
				return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The device setting could not be saved.")
			}
		}
	}
	if harness != "" {
		if err := providerkeys.ClaimForHarness(ctx, s.dataDir, name, harness); err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be saved for this harness.")
		}
	}
	// Make the key visible to local authentication probes immediately. New
	// session launches also read the durable store independently.
	if err := os.Setenv(name, secret); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be activated.")
	}
	return nil
}

// publishProviderKeys records each name as AO-published before publishing
// it, so a key in the OS user environment is never untracked: a later
// withdrawal or removal can always find it. It returns the names AO had not
// published before; on failure those are withdrawn again.
func (s *Service) publishProviderKeys(ctx context.Context, names []string, values map[string]string) ([]string, error) {
	before, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	var fresh []string
	for _, n := range names {
		if !before[n] {
			fresh = append(fresh, n)
		}
		if err := providerkeys.SetDevice(ctx, s.dataDir, n, true); err != nil {
			s.rollbackPublished(ctx, fresh)
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The device setting could not be saved.")
		}
		if err := publishProviderKey(ctx, n, values[n]); err != nil {
			s.rollbackPublished(ctx, fresh)
			return nil, apierr.Unavailable("AGENT_AUTH_DEVICE_ENV_UNAVAILABLE", "The key could not be added to this device's user environment.")
		}
	}
	return fresh, nil
}

// rollbackPublished withdraws names from the OS user environment and clears
// their marker, best effort, after a failed save.
func (s *Service) rollbackPublished(ctx context.Context, names []string) {
	for _, n := range names {
		_ = unpublishProviderKey(ctx, n)
		_ = providerkeys.SetDevice(ctx, s.dataDir, n, false)
	}
}

// withdrawProviderKey removes name from the OS user environment only when AO
// put it there, so a variable the user set themselves is left alone.
func (s *Service) withdrawProviderKey(ctx context.Context, name string) error {
	device, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	if !device[name] {
		return nil
	}
	if err := unpublishProviderKey(ctx, name); err != nil {
		return apierr.Unavailable("AGENT_AUTH_DEVICE_ENV_UNAVAILABLE", "The key could not be removed from this device's user environment.")
	}
	return nil
}

// RemoveProviderKey deletes name and its companions from AO's store, this
// daemon's environment, and the OS user environment when AO published it.
// Copies a harness wrote into its own configuration are left in place.
func (s *Service) RemoveProviderKey(ctx context.Context, name string) (ProviderKey, error) {
	if _, ok := providerKeyCatalog[name]; !ok {
		return ProviderKey{}, unknownProviderKey(name)
	}
	for _, n := range append([]string{name}, companionVariables[name]...) {
		if err := s.withdrawProviderKey(ctx, n); err != nil {
			return ProviderKey{}, err
		}
		if err := providerkeys.Remove(ctx, s.dataDir, n); err != nil {
			return ProviderKey{}, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be removed.")
		}
		_ = os.Unsetenv(n)
	}
	return s.providerKey(ctx, name)
}

// CheckProviderKey asks the provider whether it accepts secret, or the stored
// key when secret is empty.
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
