package agentauth

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

func stubDeviceEnvironment(t *testing.T) (published, withdrawn map[string]string) {
	t.Helper()
	published, withdrawn = map[string]string{}, map[string]string{}
	previousPublish, previousUnpublish := publishProviderKey, unpublishProviderKey
	publishProviderKey = func(_ context.Context, name, value, _ string) error {
		published[name] = value
		return nil
	}
	unpublishProviderKey = func(_ context.Context, name, _ string) error {
		withdrawn[name] = ""
		return nil
	}
	t.Cleanup(func() { publishProviderKey, unpublishProviderKey = previousPublish, previousUnpublish })
	allowDeviceEnvironment(t)
	return published, withdrawn
}

// allowDeviceEnvironment lets tests whose OS environment calls are stubbed
// share keys on platforms where AO does not publish them.
func allowDeviceEnvironment(t *testing.T) {
	t.Helper()
	previous := deviceSupported
	deviceSupported = true
	t.Cleanup(func() { deviceSupported = previous })
}

func TestProviderKeysListSharedProvidersWithoutValues(t *testing.T) {
	stubDeviceEnvironment(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	if _, err := svc.SaveProviderKey(t.Context(), "ANTHROPIC_API_KEY", "sk-ant-test-secret-1234", false); err != nil {
		t.Fatal(err)
	}
	keys, err := svc.ProviderKeys(t.Context())
	if err != nil || len(keys) == 0 {
		t.Fatalf("ProviderKeys = %v, %v", keys, err)
	}
	if len(keys[0].Agents) < len(keys[len(keys)-1].Agents) {
		t.Fatal("shared provider keys are not listed first")
	}
	for _, key := range keys {
		if key.Name != "ANTHROPIC_API_KEY" {
			continue
		}
		if !key.Configured || key.Hint != "1234" || key.Device || !key.Checkable || len(key.Agents) < 2 {
			t.Fatalf("Anthropic key = %+v", key)
		}
		return
	}
	t.Fatal("Anthropic key missing")
}

func TestSaveProviderKeyPublishesOnlyWhenChosen(t *testing.T) {
	published, withdrawn := stubDeviceEnvironment(t)
	t.Setenv("OPENAI_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	if _, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "openai-secret", false); err != nil {
		t.Fatal(err)
	}
	if len(published) != 0 || len(withdrawn) != 0 {
		t.Fatalf("touched device environment: published %v, withdrawn %v", published, withdrawn)
	}
	// An empty secret keeps the saved key and only changes the device setting.
	key, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "", true)
	if err != nil || !key.Device || published["OPENAI_API_KEY"] != "openai-secret" {
		t.Fatalf("publish = %+v, %v, %v", key, published, err)
	}
	key, err = svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "", false)
	if _, ok := withdrawn["OPENAI_API_KEY"]; err != nil || key.Device || !ok {
		t.Fatalf("withdraw = %+v, %v, %v", key, withdrawn, err)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "PATH", "value", false); err == nil {
		t.Fatal("accepted an unreviewed variable")
	}
	if _, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "", false); err == nil {
		t.Fatal("saved an empty key")
	}
}

func TestRemoveProviderKeyWithdrawsOnlyWhatAOPublished(t *testing.T) {
	_, withdrawn := stubDeviceEnvironment(t)
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	t.Setenv("XAI_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("openhands"), nil, nil, dir)
	if _, err := svc.StartLoginWithOptions(t.Context(), "openhands", "env_LLM_API_KEY", "test-secret", LoginOptions{Model: "openai/gpt-5", Device: new(true)}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "XAI_API_KEY", "xai-secret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveProviderKey(t.Context(), "LLM_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveProviderKey(t.Context(), "XAI_API_KEY"); err != nil {
		t.Fatal(err)
	}
	if _, ok := withdrawn["LLM_MODEL"]; !ok {
		t.Fatalf("companion model stayed in the device environment: %v", withdrawn)
	}
	if _, ok := withdrawn["XAI_API_KEY"]; ok {
		t.Fatal("withdrew a variable AO never published")
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || len(keys) != 0 || os.Getenv("LLM_API_KEY") != "" || os.Getenv("LLM_MODEL") != "" {
		t.Fatalf("keys after removal = %v, %v", keys, err)
	}
}

func TestEnvironmentLoginReusesSavedKey(t *testing.T) {
	stubDeviceEnvironment(t)
	t.Setenv("LLM_API_KEY", "")
	t.Setenv("LLM_MODEL", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("openhands"), nil, nil, dir)
	if _, err := svc.StartLoginWithModel(t.Context(), "openhands", "env_LLM_API_KEY", "", "openai/gpt-5"); err == nil {
		t.Fatal("accepted an empty key with nothing saved")
	}
	if _, err := svc.SaveProviderKey(t.Context(), "LLM_API_KEY", "saved-secret", false); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.StartLoginWithModel(t.Context(), "openhands", "env_LLM_API_KEY", "", "anthropic/claude-sonnet-4-5"); err != nil {
		t.Fatal(err)
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["LLM_API_KEY"] != "saved-secret" || keys["LLM_MODEL"] != "anthropic/claude-sonnet-4-5" {
		t.Fatalf("keys = %v, %v", keys, err)
	}
	if _, err := svc.StartLoginWithOptions(t.Context(), "codex", "browser", "", LoginOptions{Device: new(true)}); err == nil {
		t.Fatal("accepted a device setting for a native login")
	}
}

func TestHarnessPanelClaimsItsKey(t *testing.T) {
	stubDeviceEnvironment(t)
	t.Setenv("ANTHROPIC_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(foundExecutable("claude"), nil, nil, dir)
	if _, err := svc.SaveProviderKey(t.Context(), "ANTHROPIC_API_KEY", "page-key", false); err != nil {
		t.Fatal(err)
	}
	if _, withhold, _ := providerkeys.ForSession(dir, "claude-code"); len(withhold) != 1 {
		t.Fatalf("a key saved on the API keys page reached Claude Code: withhold=%v", withhold)
	}
	if _, err := svc.StartLogin(t.Context(), "claude-code", "env_ANTHROPIC_API_KEY", "panel-key"); err != nil {
		t.Fatal(err)
	}
	if inject, withhold, _ := providerkeys.ForSession(dir, "claude-code"); len(withhold) != 0 || inject["ANTHROPIC_API_KEY"] != "panel-key" {
		t.Fatalf("Claude Code's own key was withheld: inject=%v withhold=%v", inject, withhold)
	}
}

func TestFailedSaveNeverLeavesAnUntrackedDeviceKey(t *testing.T) {
	published, withdrawn := stubDeviceEnvironment(t)
	t.Setenv("GROQ_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	// Make AO's key store unwritable after the device marker exists.
	if err := os.WriteFile(filepath.Join(dir, "provider-api-keys.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "groq-secret", true); err == nil {
		t.Fatal("reported success with an unreadable key store")
	}
	device, _ := providerkeys.DeviceNames(dir)
	if _, stillPublished := published["GROQ_API_KEY"]; stillPublished {
		if _, ok := withdrawn["GROQ_API_KEY"]; !ok || device["GROQ_API_KEY"] {
			t.Fatalf("published key left behind: published=%v withdrawn=%v device=%v", published, withdrawn, device)
		}
	}
}

func TestSaveProviderKeyLeavesTheUsersOwnDeviceValue(t *testing.T) {
	stubDeviceEnvironment(t)
	publishProviderKey = func(context.Context, string, string, string) error { return providerkeys.ErrUserValue }
	t.Setenv("OPENAI_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	_, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "ao-secret", true)
	var apiErr *apierr.Error
	if !errors.As(err, &apiErr) || apiErr.Code != "AGENT_AUTH_DEVICE_ENV_TAKEN" {
		t.Fatalf("err = %v, want AGENT_AUTH_DEVICE_ENV_TAKEN", err)
	}
	keys, _ := providerkeys.Load(dir)
	device, _ := providerkeys.DeviceNames(dir)
	if keys["OPENAI_API_KEY"] != "" || device["OPENAI_API_KEY"] {
		t.Fatalf("refused save left state: keys=%v device=%v", keys, device)
	}
}

func TestSaveProviderKeyReplacesOnlyAOsOwnDeviceCopy(t *testing.T) {
	stubDeviceEnvironment(t)
	var previous []string
	publishProviderKey = func(_ context.Context, _, _, prior string) error {
		previous = append(previous, prior)
		return nil
	}
	t.Setenv("OPENAI_API_KEY", "")
	svc := NewWithAgentResolver(nil, nil, nil, t.TempDir())
	if _, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "first-secret", false); err != nil {
		t.Fatal(err)
	}
	// A key saved without sharing was never AO's copy in the OS environment.
	if _, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "second-secret", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "OPENAI_API_KEY", "third-secret", true); err != nil {
		t.Fatal(err)
	}
	if len(previous) != 2 || previous[0] != "" || previous[1] != "second-secret" {
		t.Fatalf("previous values = %q", previous)
	}
}

func TestDeviceWithdrawalNeverBlocksRemoval(t *testing.T) {
	stubDeviceEnvironment(t)
	var withdrawnValue string
	unpublishProviderKey = func(_ context.Context, _, value string) error {
		withdrawnValue = value
		return errors.New("no user bus")
	}
	t.Setenv("GROQ_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	key, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "groq-secret", true)
	if err != nil || !key.DeviceAvailable {
		t.Fatalf("save = %+v, %v", key, err)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "", false); err != nil {
		t.Fatalf("turning sharing off failed: %v", err)
	}
	if withdrawnValue != "groq-secret" {
		t.Fatalf("withdrew %q, want AO's copy", withdrawnValue)
	}
	if _, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "", true); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.RemoveProviderKey(t.Context(), "GROQ_API_KEY"); err != nil {
		t.Fatalf("removal failed: %v", err)
	}
	keys, err := providerkeys.Load(dir)
	if err != nil || keys["GROQ_API_KEY"] != "" {
		t.Fatalf("key survived removal: %v, %v", keys, err)
	}
}

func TestFailedSaveRestoresTheEarlierSharedValue(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("needs a data directory the test user cannot write")
	}
	published, _ := stubDeviceEnvironment(t)
	t.Setenv("GROQ_API_KEY", "")
	dir := t.TempDir()
	svc := NewWithAgentResolver(nil, nil, nil, dir)
	if _, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "groq-old", true); err != nil {
		t.Fatal(err)
	}
	// The device marker is already set, so only the key store write fails.
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o700) })
	if _, err := svc.SaveProviderKey(t.Context(), "GROQ_API_KEY", "groq-new", true); err == nil {
		t.Fatal("reported success with an unwritable key store")
	}
	keys, _ := providerkeys.Load(dir)
	if keys["GROQ_API_KEY"] != "groq-old" || published["GROQ_API_KEY"] != "groq-old" {
		t.Fatalf("store has %q but the user environment has %q", keys["GROQ_API_KEY"], published["GROQ_API_KEY"])
	}
}
