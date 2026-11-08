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
	// DeviceAvailable reports that this daemon's platform supports Device.
	DeviceAvailable bool `json:"deviceAvailable"`
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
	if err := s.storeProviderKey(ctx, name, secret, &device, ""); err != nil {
		return ProviderKey{}, err
	}
	return s.providerKey(ctx, name)
}

// storeProviderKey saves name (and, when secret is empty, reuses the stored
// value), activates it for this daemon, and publishes or withdraws it from the
// OS user environment together with its companions. A non-empty harness is
// the harness whose own panel saved it, which lets that harness's sessions
// receive a key that would otherwise replace its subscription sign-in. A nil
// share keeps the device setting the key already has, so a client that has
// not loaded it yet never withdraws a published key by omission.
func (s *Service) storeProviderKey(ctx context.Context, name, secret string, share *bool, harness string) error {
	if share != nil && *share && !deviceSupported {
		return apierr.Invalid("AGENT_AUTH_DEVICE_ENV_UNSUPPORTED", "Sharing a key with other apps isn't available on this device.", nil)
	}
	keys, err := providerkeys.Load(s.dataDir)
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	device := share != nil && *share
	if share == nil {
		current, err := providerkeys.DeviceNames(s.dataDir)
		if err != nil {
			return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
		}
		device = current[name] && keys[name] != "" && deviceSupported
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
	undoPublish := func() {}
	if device {
		if undoPublish, err = s.publishProviderKeys(ctx, present, values, keys); err != nil {
			return err
		}
	} else {
		for _, n := range present {
			if err := s.withdrawProviderKey(ctx, n, keys[n]); err != nil {
				return err
			}
		}
	}
	if err := providerkeys.Save(ctx, s.dataDir, name, secret); err != nil {
		undoPublish()
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
	if err := providerkeys.ExportForProbes(name, secret); err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be activated.")
	}
	return nil
}

// releaseSubscriptionKey stops passing agentID the variable that would
// override its own sign-in (ANTHROPIC_API_KEY for Claude Code, and so on)
// after it signs in another way. The key stays saved for other harnesses.
// It returns the variable it released ("" when none) so a sign-in that then
// fails can restore the claim with restoreSubscriptionKey.
func (s *Service) releaseSubscriptionKey(ctx context.Context, agentID string) (string, error) {
	if strings.TrimSpace(s.dataDir) == "" {
		return "", nil
	}
	return providerkeys.ReleaseHarness(ctx, s.dataDir, agentID)
}

// restoreSubscriptionKey gives agentID back the claim releaseSubscriptionKey
// withdrew, after the sign-in that replaced it failed.
func (s *Service) restoreSubscriptionKey(ctx context.Context, agentID, released string) error {
	if released == "" {
		return nil
	}
	return providerkeys.ClaimForHarness(ctx, s.dataDir, released, agentID)
}

// publishProviderKeys records each name as AO-published before publishing
// it, so a key in the OS user environment is never untracked: a later
// withdrawal or removal can always find it. stored holds the values saved
// before this call, so AO may replace its own earlier copy but never a
// different value the user set themselves. The returned undo, also run on
// failure here, withdraws the names AO had not published before and puts
// AO's earlier copy back where this call replaced it, so the OS environment
// again matches the store when a later save fails.
func (s *Service) publishProviderKeys(ctx context.Context, names []string, values, stored map[string]string) (undo func(), err error) {
	before, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	// fresh holds only names AO actually published here for the first time,
	// so undo never unsets a value AO did not put in the environment.
	var fresh []string
	replaced := map[string]string{}
	undo = func() {
		// The save may have failed because ctx ended; cleanup still runs.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		s.rollbackPublished(cleanupCtx, fresh, values)
		for n, earlier := range replaced {
			_ = publishProviderKey(cleanupCtx, n, earlier, values[n])
		}
	}
	for _, n := range names {
		previous := ""
		if before[n] {
			previous = stored[n]
		}
		if err := providerkeys.SetDevice(ctx, s.dataDir, n, true); err != nil {
			undo()
			return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The device setting could not be saved.")
		}
		if err := publishProviderKey(ctx, n, values[n], previous); err != nil {
			// Nothing was published for n: drop only the marker it gained.
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
		if !before[n] {
			fresh = append(fresh, n)
		} else if previous != "" && previous != values[n] {
			replaced[n] = previous
		}
	}
	return undo, nil
}

// rollbackPublished withdraws names from the OS user environment and clears
// their marker, best effort, after a failed save. values holds the copies
// AO tried to publish; a different value is left in place.
func (s *Service) rollbackPublished(ctx context.Context, names []string, values map[string]string) {
	for _, n := range names {
		_ = unpublishProviderKey(ctx, n, values[n])
		_ = providerkeys.SetDevice(ctx, s.dataDir, n, false)
	}
}

// withdrawProviderKey removes name from the OS user environment only when AO
// put it there and it still holds value, AO's copy, so a variable the user set
// themselves is left alone. That environment is session-scoped and AO stops
// republishing the key once its marker is cleared, so an unreachable user
// manager (SSH, headless, WSL) never blocks turning sharing off or removal.
func (s *Service) withdrawProviderKey(ctx context.Context, name, value string) error {
	device, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	if device[name] {
		_ = unpublishProviderKey(ctx, name, value)
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
	keys, err := providerkeys.Load(s.dataDir)
	if err != nil {
		return ProviderKey{}, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	for _, n := range append([]string{name}, companionVariables[name]...) {
		if err := s.withdrawProviderKey(ctx, n, keys[n]); err != nil {
			return ProviderKey{}, err
		}
		if err := providerkeys.Remove(ctx, s.dataDir, n); err != nil {
			return ProviderKey{}, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be removed.")
		}
		// Put back what the user exported before AO replaced it, if anything.
		_ = providerkeys.RestoreInherited(n)
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
