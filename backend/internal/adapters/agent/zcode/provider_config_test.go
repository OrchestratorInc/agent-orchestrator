package zcode

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const testAPIKey = "zai-test-key-abc123"

func TestPersonalProviderConfigPath(t *testing.T) {
	if got, want := PersonalProviderConfigPath("/tmp/ao-data"), filepath.Join("/tmp/ao-data", "harnesses", "zcode", "provider_config.json"); got != want {
		t.Fatalf("path = %q, want %q", got, want)
	}
	if got := PersonalProviderConfigPath("  "); got != "" {
		t.Fatalf("path for empty data dir = %q, want empty", got)
	}
}

func TestWritePersonalProviderConfig(t *testing.T) {
	ctx := context.Background()
	path := PersonalProviderConfigPath(t.TempDir())
	key := []byte(" " + testAPIKey + "\n")
	if err := WritePersonalProviderConfig(ctx, path, key); err != nil {
		t.Fatalf("WritePersonalProviderConfig: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if runtime.GOOS != "windows" {
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode = %o, want 0600", got)
		}
		dirInfo, err := os.Stat(filepath.Dir(path))
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("dir mode = %o, want 0700", got)
		}
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("parse: %v\n%s", err, data)
	}
	if doc["schemaVersion"] != float64(1) {
		t.Fatalf("schemaVersion = %v", doc["schemaVersion"])
	}
	config := doc["config"].(map[string]any)
	rules := config["providerConfigRules"].(map[string]any)["providerRules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("providerRules = %v, want one rule", rules)
	}
	rule := rules[0].(map[string]any)
	if rule["providerId"] != "zai-api-ao" || rule["templateId"] != "zai-api" || rule["providerName"] != "Z.ai Coding Plan (AO)" {
		t.Fatalf("rule identity = %v", rule)
	}
	ruleConfig := rule["config"].(map[string]any)
	access := ruleConfig["access"].(map[string]any)
	if ruleConfig["group"] != "standard-personal" || access["type"] != "zhipu-coding-plan-api-key" || access["apiKey"] != testAPIKey {
		t.Fatalf("rule config = %v", ruleConfig)
	}
	models := ruleConfig["personalModelIds"].([]any)
	if len(models) != 2 || models[0] != "GLM-5.3" || models[1] != "GLM-5.3-Flash" {
		t.Fatalf("personalModelIds = %v", models)
	}
	modelRules := config["modelConfigRules"].(map[string]any)
	if got := modelRules["providerModelRules"].([]any); len(got) != 0 {
		t.Fatalf("providerModelRules = %v, want empty list", got)
	}
	if got := modelRules["manualProviderModelRules"].([]any); len(got) != 0 {
		t.Fatalf("manualProviderModelRules = %v, want empty list", got)
	}
	selection := config["defaultModelSelection"].(map[string]any)
	if selection["providerId"] != "zai-api-ao" || selection["modelId"] != "GLM-5.3" {
		t.Fatalf("defaultModelSelection = %v", selection)
	}
	if strings.Contains(string(data), "null") {
		t.Fatalf("provider config contains null: %s", data)
	}

	hasKey, err := providerConfigHasKey(ctx, path)
	if err != nil || !hasKey {
		t.Fatalf("providerConfigHasKey = %v, %v; want true", hasKey, err)
	}

	// Rewriting replaces the file atomically with the same mode.
	if err := WritePersonalProviderConfig(ctx, path, []byte("other-key")); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	leftovers, _ := filepath.Glob(filepath.Join(filepath.Dir(path), ".ao-tmp-*"))
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}

	t.Run("tightens a directory the zcode CLI created first", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("POSIX directory modes")
		}
		// The common path: a browser login or session launch already pinned
		// ZCODE_DATA_BASE_DIR at this directory and the CLI mkdir'd it 0755.
		path := PersonalProviderConfigPath(t.TempDir())
		dir := filepath.Dir(path)
		if err := os.MkdirAll(dir, 0o755); err != nil { //nolint:gosec // simulating the CLI's umask
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0o755); err != nil { //nolint:gosec // simulating the CLI's umask
			t.Fatal(err)
		}
		if err := WritePersonalProviderConfig(ctx, path, []byte(testAPIKey)); err != nil {
			t.Fatalf("WritePersonalProviderConfig: %v", err)
		}
		dirInfo, err := os.Stat(dir)
		if err != nil {
			t.Fatalf("stat dir: %v", err)
		}
		if got := dirInfo.Mode().Perm(); got != 0o700 {
			t.Fatalf("pre-existing dir mode = %o after write, want 0700", got)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat: %v", err)
		}
		if got := info.Mode().Perm(); got != 0o600 {
			t.Fatalf("mode = %o, want 0600", got)
		}
	})
}

func TestWritePersonalProviderConfigRejectsBadInput(t *testing.T) {
	ctx := context.Background()
	path := PersonalProviderConfigPath(t.TempDir())
	if err := WritePersonalProviderConfig(ctx, path, []byte("  \n")); err == nil {
		t.Fatal("empty key accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("empty key wrote a file: %v", err)
	}
	if err := WritePersonalProviderConfig(ctx, "", []byte(testAPIKey)); err == nil {
		t.Fatal("empty path accepted")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := WritePersonalProviderConfig(cancelled, path, []byte(testAPIKey)); err == nil {
		t.Fatal("cancelled context accepted")
	}
}

func TestProviderConfigHasKey(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	missing := filepath.Join(dir, "missing.json")
	missingCtx, missingEvidence := ports.WithAuthEvidence(ctx)
	if hasKey, err := providerConfigHasKey(missingCtx, missing); err != nil || hasKey {
		t.Fatalf("missing file: hasKey=%v err=%v, want false,nil", hasKey, err)
	}
	// The AO-owned file is absent for every OAuth-only user; its absence is
	// not a credential-store problem and must not surface a diagnostic.
	if got := missingEvidence.Diagnostics(); len(got) != 0 {
		t.Fatalf("missing file recorded diagnostics: %+v, want none", got)
	}
	empty := filepath.Join(dir, "empty.json")
	if err := os.WriteFile(empty, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasKey, err := providerConfigHasKey(ctx, empty); err != nil || hasKey {
		t.Fatalf("empty file: hasKey=%v err=%v, want false,nil", hasKey, err)
	}
	blank := filepath.Join(dir, "blank.json")
	if err := os.WriteFile(blank, []byte(`{"schemaVersion":1,"config":{"providerConfigRules":{"providerRules":[{"providerId":"zai-api-ao","config":{"access":{"type":"zhipu-coding-plan-api-key","apiKey":"  "}}}]}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if hasKey, err := providerConfigHasKey(ctx, blank); err != nil || hasKey {
		t.Fatalf("blank key: hasKey=%v err=%v, want false,nil", hasKey, err)
	}
	invalid := filepath.Join(dir, "invalid.json")
	if err := os.WriteFile(invalid, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	evidenceCtx, evidence := ports.WithAuthEvidence(ctx)
	if hasKey, err := providerConfigHasKey(evidenceCtx, invalid); err == nil || hasKey {
		t.Fatalf("invalid JSON: hasKey=%v err=%v, want false,error", hasKey, err)
	}
	if !hasDiagnostic(evidence, "credential_file_invalid") {
		t.Fatalf("invalid JSON recorded no credential_file_invalid diagnostic")
	}
}

// hasDiagnostic reports whether the collected evidence carries a diagnostic
// with the given code.
func hasDiagnostic(evidence *ports.AuthEvidence, code string) bool {
	for _, step := range evidence.Diagnostics() {
		if step.Code == code {
			return true
		}
	}
	return false
}

func TestAuthStatusPrefersAPIKeyProviderConfig(t *testing.T) {
	ctx := context.Background()

	t.Run("API key present is authorized with file evidence", func(t *testing.T) {
		// An expired OAuth token in the store must not demote the API key.
		writeStore(t, map[string]string{zcodeAccessTokenKey: sealCredential(t, jwtWithClaims(t, map[string]any{"exp": 1, "sub": "u"}))})
		dataDir := isolateAODataDir(t)
		path := PersonalProviderConfigPath(dataDir)
		if err := WritePersonalProviderConfig(ctx, path, []byte(testAPIKey)); err != nil {
			t.Fatal(err)
		}
		evidenceCtx, evidence := ports.WithAuthEvidence(ctx)
		status, err := New().AuthStatus(evidenceCtx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = %q, want authorized", status)
		}
		identity := evidence.Identity()
		if identity.Method != domain.AgentAccountMethodAPIKey || identity.SourceKind != domain.AgentCredentialSourceFile {
			t.Fatalf("evidence = %#v, want API key file evidence", identity)
		}
		if !strings.HasSuffix(identity.Source, filepath.Join("harnesses", "zcode", "provider_config.json")) {
			t.Fatalf("evidence source = %q, want the provider config path", identity.Source)
		}
	})

	t.Run("invalid provider config is unknown", func(t *testing.T) {
		dataDir := isolateAODataDir(t)
		t.Setenv(zcodeDataBaseDirEnv, t.TempDir())
		t.Setenv(zcodeCredentialSecretEnv, testSecret)
		path := PersonalProviderConfigPath(dataDir)
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("{broken"), 0o600); err != nil {
			t.Fatal(err)
		}
		evidenceCtx, evidence := ports.WithAuthEvidence(ctx)
		status, err := New().AuthStatus(evidenceCtx)
		if err == nil {
			t.Fatal("invalid provider config returned no error")
		}
		if status != ports.AgentAuthStatusUnknown {
			t.Fatalf("status = %q, want unknown", status)
		}
		if !hasDiagnostic(evidence, "credential_file_invalid") {
			t.Fatal("invalid provider config recorded no credential_file_invalid diagnostic")
		}
	})

	t.Run("absent provider config falls through to the OAuth store", func(t *testing.T) {
		valid := jwtWithClaims(t, map[string]any{"exp": 4102444800, "sub": "u"})
		writeStore(t, map[string]string{zcodeAccessTokenKey: sealCredential(t, valid)})
		evidenceCtx, evidence := ports.WithAuthEvidence(ctx)
		status, err := New().AuthStatus(evidenceCtx)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if status != ports.AgentAuthStatusAuthorized {
			t.Fatalf("status = %q, want authorized via OAuth", status)
		}
		if got := evidence.Identity().Method; got != domain.AgentAccountMethodAccount {
			t.Fatalf("evidence method = %q, want account (OAuth branch)", got)
		}
		if hasDiagnostic(evidence, "credential_file_missing") {
			t.Fatalf("absent provider config recorded credential_file_missing: %+v", evidence.Diagnostics())
		}
	})
}

func TestAugmentRuntimeLaunchEnvExportsProviderConfig(t *testing.T) {
	plugin := New()
	t.Setenv(zcodeDataBaseDirEnv, "")

	t.Run("sets the path when the file exists", func(t *testing.T) {
		dataDir := t.TempDir()
		path := PersonalProviderConfigPath(dataDir)
		if err := WritePersonalProviderConfig(context.Background(), path, []byte(testAPIKey)); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{}
		plugin.AugmentRuntimeLaunchEnv(env, dataDir, "sess", "launch")
		if env[zcodePersonalProviderConfigEnv] != path {
			t.Fatalf("env = %v, want %s=%s", env, zcodePersonalProviderConfigEnv, path)
		}
		if env[zcodeDataBaseDirEnv] != filepath.Join(dataDir, "harnesses", "zcode") {
			t.Fatalf("env = %v, want the store base alongside", env)
		}
		for _, value := range env {
			if strings.Contains(value, testAPIKey) {
				t.Fatalf("API key leaked into launch env: %v", env)
			}
		}
	})

	t.Run("leaves env alone when the file is absent", func(t *testing.T) {
		env := map[string]string{}
		plugin.AugmentRuntimeLaunchEnv(env, t.TempDir(), "sess", "launch")
		if _, ok := env[zcodePersonalProviderConfigEnv]; ok {
			t.Fatalf("env = %v, want no provider config injection", env)
		}
	})

	// Launch and probe must agree: a file the probe would not treat as the
	// credential is not exported to the CLI either.
	for name, contents := range map[string]string{
		"unparseable file is not exported": "{broken",
		"blank key is not exported":        `{"schemaVersion":1,"config":{"providerConfigRules":{"providerRules":[{"providerId":"zai-api-ao","config":{"access":{"type":"zhipu-coding-plan-api-key","apiKey":"  "}}}]}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			dataDir := t.TempDir()
			path := PersonalProviderConfigPath(dataDir)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			env := map[string]string{}
			plugin.AugmentRuntimeLaunchEnv(env, dataDir, "sess", "launch")
			if _, ok := env[zcodePersonalProviderConfigEnv]; ok {
				t.Fatalf("env = %v, want no provider config injection", env)
			}
			if env[zcodeDataBaseDirEnv] != filepath.Join(dataDir, "harnesses", "zcode") {
				t.Fatalf("env = %v, want the store base still pinned", env)
			}
		})
	}

	t.Run("preset value wins", func(t *testing.T) {
		dataDir := t.TempDir()
		if err := WritePersonalProviderConfig(context.Background(), PersonalProviderConfigPath(dataDir), []byte(testAPIKey)); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{zcodePersonalProviderConfigEnv: "/opt/keep.json"}
		plugin.AugmentRuntimeLaunchEnv(env, dataDir, "sess", "launch")
		if env[zcodePersonalProviderConfigEnv] != "/opt/keep.json" {
			t.Fatalf("env = %v, want preset provider config preserved", env)
		}
	})

	t.Run("explicit store override still exports the provider config", func(t *testing.T) {
		dataDir := t.TempDir()
		path := PersonalProviderConfigPath(dataDir)
		if err := WritePersonalProviderConfig(context.Background(), path, []byte(testAPIKey)); err != nil {
			t.Fatal(err)
		}
		env := map[string]string{zcodeDataBaseDirEnv: "/opt/keep"}
		plugin.AugmentRuntimeLaunchEnv(env, dataDir, "sess", "launch")
		if env[zcodeDataBaseDirEnv] != "/opt/keep" || env[zcodePersonalProviderConfigEnv] != path {
			t.Fatalf("env = %v, want store override kept and provider config exported", env)
		}
	})
}
