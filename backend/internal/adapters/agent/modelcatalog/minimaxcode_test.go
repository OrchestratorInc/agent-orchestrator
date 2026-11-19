package modelcatalog

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

func TestMiniMaxConfiguredModels(t *testing.T) {
	data := []byte(`{"providers":[{"providerId":"custom_provider:zai","enabled":true,"hasApiKey":true,"models":[{"modelId":"glm-5.3-flash","selected":true}]},{"providerId":"minimax_oauth","enabled":true,"models":[]}]}`)
	got, err := parseMiniMaxModels(data)
	if err != nil || len(got) != 1 || got[0].ID != "custom_provider:zai/glm-5.3-flash" || !got[0].IsDefault {
		t.Fatalf("models=%v, %v", got, err)
	}
	if Base("minimax-code").CustomModelEntry != ports.CustomModelEntryConfigured {
		t.Fatal("MiniMax must use configured model IDs")
	}
}

// Without a clean interrupt, discovery must fail rather than report an empty
// catalog that would replace cached models.
func TestMiniMaxDiscoveryUnavailableWithoutInterrupt(t *testing.T) {
	previous := commandInterruptSupported
	commandInterruptSupported = false
	t.Cleanup(func() { commandInterruptSupported = previous })
	got, err := discoverCommandCatalog(context.Background(), "minimax-code", "/nonexistent/mcode", t.TempDir(), nil, true)
	if err == nil {
		t.Fatalf("discovery succeeded with %d models; want unavailable error", len(got.Models))
	}
}

// Editing the MiniMax profile's configured models must invalidate the cached
// catalog, since `mcode provider list` reports them from config.yaml.
func TestMiniMaxFingerprintTracksProfileConfig(t *testing.T) {
	profile := t.TempDir()
	env := map[string]string{"MINIMAX_DATA_DIR": profile}
	write := func(body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(profile, "config.yaml"), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("defaultModel: custom_provider:ao-a/model-a\n")
	before := CatalogFingerprint(context.Background(), "minimax-code", "", t.TempDir(), env)
	if again := CatalogFingerprint(context.Background(), "minimax-code", "", t.TempDir(), env); again != before {
		t.Fatalf("unchanged config changed the fingerprint: %s != %s", again, before)
	}
	write("defaultModel: custom_provider:ao-b/model-b\n")
	if after := CatalogFingerprint(context.Background(), "minimax-code", "", t.TempDir(), env); after == before {
		t.Fatal("editing the MiniMax profile config left the catalog fingerprint unchanged")
	}
}

// A HOME in the request environment moves MiniMax's default profile, so the
// fingerprint must read the config under that HOME, not the daemon's.
func TestMiniMaxFingerprintFollowsRequestHome(t *testing.T) {
	home := t.TempDir()
	key := "HOME"
	if runtime.GOOS == "windows" {
		key = "USERPROFILE"
	}
	env := map[string]string{key: home, "MINIMAX_DATA_DIR": "", "MAVIS_DATA_DIR": ""}
	config := filepath.Join(home, ".minimax", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(config), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config, []byte("defaultModel: a\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	before := CatalogFingerprint(context.Background(), "minimax-code", "", t.TempDir(), env)
	if err := os.WriteFile(config, []byte("defaultModel: b\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if after := CatalogFingerprint(context.Background(), "minimax-code", "", t.TempDir(), env); after == before {
		t.Fatal("editing config under the request HOME left the fingerprint unchanged")
	}
}
