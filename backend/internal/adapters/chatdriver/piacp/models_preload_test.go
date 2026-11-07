package piacp

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestModelsPreloadSupportsNewProvidersWithoutInvalidatingTheCatalogue(t *testing.T) {
	node, err := exec.LookPath("node")
	if err != nil {
		t.Skip("Node is required to exercise the Pi JavaScript preload")
	}
	dir := t.TempDir()
	store := `{"azure-openai-responses":{"models":[{"id":"azure-model","api":"azure-openai-responses","baseUrl":""}]},"zai-coding-cn":{"models":[{"id":"glm-5.3-flash","api":"openai-completions","baseUrl":"https://example.test/v4","compat":{"supportsDeveloperRole":false,"thinkingFormat":"zai","zaiToolStream":true}}]},"signed-out-provider":{"models":[{"id":"unavailable","api":"openai-completions","baseUrl":"https://example.test/v4"}]}}`
	if err := os.WriteFile(filepath.Join(dir, "models-store.json"), []byte(store), 0o600); err != nil {
		t.Fatal(err)
	}
	// Exercise the actual preload with a local store and fake auth, without
	// installing Pi, contacting a provider, or reading the user's credentials.
	script := append([]byte(nil), modelsPreload...)
	script = append(script, []byte(`
const path = mergedModelsPath(process.argv[2], {get: (provider) => provider === "zai-coding-cn" ? {type: "api_key", key: "test-only-key"} : undefined}, new Set(["azure-openai-responses"]));
console.log(readFileSync(path, "utf-8"));
`)...)
	entry := filepath.Join(dir, "test.mjs")
	if err := os.WriteFile(entry, script, 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(node, entry, filepath.Join(dir, "models.json"))
	cmd.Env = append(os.Environ(), "AO_PI_ACP_ENTRY=", "AO_PI_MODELS_JSON="+filepath.Join(dir, "merged.json"))
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("preload: %v: %s", err, output)
	}
	var got struct {
		Providers map[string]struct {
			BaseURL string           `json:"baseUrl"`
			APIKey  string           `json:"apiKey"`
			Models  []map[string]any `json:"models"`
		} `json:"providers"`
	}
	if err := json.Unmarshal(output, &got); err != nil {
		t.Fatalf("preload output: %v: %s", err, output)
	}
	if _, set := got.Providers["azure-openai-responses"].Models[0]["baseUrl"]; set {
		t.Error("empty optional baseUrl invalidates the complete models.json")
	}
	zai := got.Providers["zai-coding-cn"]
	if zai.BaseURL != "https://example.test/v4" || zai.APIKey != "test-only-key" || len(zai.Models) != 1 {
		t.Errorf("new provider lacks the endpoint/auth required by the older Pi: %#v", zai)
	}
	compat, ok := zai.Models[0]["compat"].(map[string]any)
	if !ok || compat["supportsDeveloperRole"] != false || compat["thinkingFormat"] != "zai" {
		t.Errorf("provider request compatibility was dropped: %#v", compat)
	}
	if _, set := compat["zaiToolStream"]; set {
		t.Error("a newer compatibility field would invalidate the older schema")
	}
	if _, set := got.Providers["signed-out-provider"]; set {
		t.Error("a new provider without credentials would invalidate the complete catalogue")
	}
}
