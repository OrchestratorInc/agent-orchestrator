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
	// WithdrawFailed, set only on the answer to a save or removal, reports that
	// AO could not take its copy back out of the OS user environment (no user
	// session manager, say, over SSH). The save or removal itself succeeded;
	// programs started outside AO may still see the key until the user signs
	// out.
	WithdrawFailed bool `json:"withdrawFailed,omitempty"`
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

// storeProviderKeyReporting saves name (and, when secret is empty, reuses the stored
// value), activates it for this daemon, and publishes or withdraws it from the
// OS user environment together with its companions. A non-empty harness is
// the harness whose own panel saved it, which lets that harness's sessions
// receive a key that would otherwise replace its subscription sign-in. A nil
// share keeps the device setting the key already has, so a client that has
// not loaded it yet never withdraws a published key by omission. It reports
// whether AO could not take its copy back out of the OS user environment, so
// the login panel can say so.
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
	// Make the key visible to local authentication probes immediately. New
	// session launches also read the durable store independently.
	if err := providerkeys.ExportForProbes(name, secret); err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "The provider key could not be activated.")
	}
	return withdrawFailed, nil
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
	users, err := providerkeys.UserValueNames(s.dataDir)
	if err != nil {
		return nil, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	// fresh holds only names AO actually published here for the first time,
	// so undo never unsets a value AO did not put in the environment;
	// usersHere the new names whose value the user already had; reclaimed the
	// names that were the user's until AO published its own value here.
	var fresh, usersHere, reclaimed []string
	replaced := map[string]string{}
	undo = func() {
		// The save may have failed because ctx ended; cleanup still runs.
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		s.rollbackPublished(cleanupCtx, fresh, values)
		for _, n := range usersHere {
			_ = providerkeys.SetDevice(cleanupCtx, s.dataDir, n, false)
		}
		// The user's value had gone, so only AO's copy is there: take it back
		// out and leave the name as it was before this save.
		for _, n := range reclaimed {
			_ = unpublishProviderKey(cleanupCtx, n, values[n])
			_ = providerkeys.SetUserValue(cleanupCtx, s.dataDir, n, true)
		}
		for n, earlier := range replaced {
			_ = publishProviderKey(cleanupCtx, n, earlier, values[n])
		}
	}
	for _, n := range names {
		// The value in the environment is AO's earlier copy only when AO
		// published it; a value the user had is never treated as AO's.
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
			// Shared already, by the user: record it so AO never removes it.
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
		if users[n] {
			// The value was the user's, but Publish found it gone and AO's
			// copy is now there: it is AO's to withdraw from here on.
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
// failed reports that AO's copy may still be there, so the caller can tell
// the user it stays visible outside AO until they sign out.
func (s *Service) withdrawProviderKey(ctx context.Context, name, value string) (failed bool, err error) {
	device, err := providerkeys.DeviceNames(s.dataDir)
	if err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	users, err := providerkeys.UserValueNames(s.dataDir)
	if err != nil {
		return false, apierr.Unavailable("AGENT_AUTH_STORE_UNAVAILABLE", "AO's provider key store could not be read.")
	}
	// A value the user had set before AO shared it stays theirs.
	if device[name] && !users[name] {
		// Best effort: a failure is reported, never returned, so it cannot
		// block turning sharing off or removal.
		failed = unpublishProviderKey(ctx, name, value) != nil
	}
	return failed, nil
}

// RemoveProviderKey deletes name and its companions from AO's store, this
// daemon's environment, and the OS user environment when AO published it.
// Copies a harness wrote into its own configuration are left in place.
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

// removeProviderKey does RemoveProviderKey's work under keysMu, so the
// startup republication cannot publish a key again after it is removed. It
// reports whether a withdrawal from the OS user environment failed.
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
		// Put back what the user exported before AO replaced it, if anything.
		_ = providerkeys.RestoreInherited(n)
	}
	return withdrawFailed, nil
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
