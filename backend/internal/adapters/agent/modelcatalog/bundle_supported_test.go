package modelcatalog

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeBundleFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestClineSupportedProvidersReadsInstalledRegistry(t *testing.T) {
	root := filepath.Join(t.TempDir(), "cline")
	writeBundleFile(t, filepath.Join(root, "bin", "cline"), "#!/usr/bin/env node\n")
	providers := filepath.Join(root, "node_modules", "@cline", "llms", "dist", "providers")
	writeBundleFile(t, filepath.Join(providers, "ids.d.ts"), `export declare enum BUILT_IN_PROVIDER {
    ANTHROPIC = "anthropic",
    CLINE = "cline",
    ZAI = "zai"
}`)
	writeBundleFile(t, filepath.Join(providers, "provider-ids.generated.d.ts"), `export declare const GENERATED_PROVIDER_IDS: readonly ["302ai", "zai", "venice"];`)

	got := clineSupportedProviders(filepath.Join(root, "bin", "cline"))
	if want := []string{"anthropic", "cline", "zai", "302ai", "venice"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
	if got := clineSupportedProviders(filepath.Join(t.TempDir(), "bin", "cline")); got != nil {
		t.Fatalf("missing registry = %v", got)
	}
}

func TestContinueSupportedProvidersReadsProviderSwitch(t *testing.T) {
	dist := filepath.Join(t.TempDir(), "dist")
	writeBundleFile(t, filepath.Join(dist, "cn.js"), "#!/usr/bin/env node\n")
	writeBundleFile(t, filepath.Join(dist, "index.js"),
		`function a(e){switch(e.kind){case"x":return 1}}`+
			`function b(e){switch(e.provider){case"openai":return new A(e);case"zAI":{let t=1;return new B(e)}case"mock":return new M(e);case"llama.cpp":case"llamafile":return new L(e);default:return}}`)

	got := continueSupportedProviders(filepath.Join(dist, "cn.js"))
	if want := []string{"openai", "zAI", "llama.cpp", "llamafile"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("providers = %v, want %v", got, want)
	}
}
