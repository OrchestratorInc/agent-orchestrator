package mastracode

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

// The fake packages expose the reviewed public SDK boundary. Model calls assert
// their actual options and provider; they never use a network or credential file.
func fixtureNativeAuth(t *testing.T, credential, model, outcome string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("POSIX native executable fixture")
	}
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node is unavailable")
	}
	root := t.TempDir()
	write := func(path, body string) {
		t.Helper()
		path = filepath.Join(root, path)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0700); err != nil {
			t.Fatal(err)
		}
	}
	write("node_modules/mastracode/package.json", `{"name":"mastracode","version":"0.45.0","type":"module"}`)
	write("node_modules/mastracode/dist/cli.js", "#!/usr/bin/env node\n")
	write("node_modules/@mastra/code-sdk/package.json", `{"name":"@mastra/code-sdk","version":"1.11.0","type":"module","exports":{"./package.json":"./package.json","./*":{"import":{"default":"./dist/*.js"}}}}`)
	write("node_modules/@mastra/core/package.json", `{"name":"@mastra/core","version":"1.75.0","type":"module","exports":{"./package.json":"./package.json","./llm":"./llm.js"}}`)
	write("node_modules/@mastra/core/llm.js", `export const PROVIDER_REGISTRY={zai:{apiKeyEnvVar:'ZHIPU_API_KEY'},minimax:{apiKeyEnvVar:'MINIMAX_API_KEY'}};`)
	write("node_modules/@mastra/code-sdk/dist/auth/storage.js", `export class AuthStorage {
 list(){return process.env.AO_TEST_MASTRA_CREDENTIAL==='stored'?['apikey:zai']:process.env.AO_TEST_MASTRA_CREDENTIAL==='oauth'?['zai']:[]}
 get(provider){return process.env.AO_TEST_MASTRA_CREDENTIAL==='oauth'&&provider==='zai'?{type:'oauth'}:undefined}
 getStoredApiKey(provider){return process.env.AO_TEST_MASTRA_CREDENTIAL==='stored'&&provider==='zai'?'fixture-secret':undefined}
 loadStoredApiKeysIntoEnv(vars){for(const [provider,env] of Object.entries(vars)){const key=this.getStoredApiKey(provider);if(key&&!process.env[env])process.env[env]=key}}
 }`)
	write("node_modules/@mastra/code-sdk/dist/onboarding/settings.js", `export function stripMastraCodeCustomProviderPrefix(id){return id}; export function loadSettings(){return {models:{modeDefaults:{build:process.env.AO_TEST_MASTRA_MODEL}},customProviders:process.env.AO_TEST_MASTRA_CREDENTIAL==='custom'?[{name:'local',url:'https://example.invalid',apiKey:'fixture-secret',models:['test']}]:[]}}`)
	write("node_modules/@mastra/code-sdk/dist/agents/model.js", `export function resolveModel(id){if(!['zai/glm-test','minimax/model-test','local/test'].includes(id))throw Error('unexpected selected provider');return {modelId:id}}`)
	write("node_modules/ai/package.json", `{"name":"ai","type":"module","exports":"./index.js"}`)
	write("node_modules/ai/index.js", `export async function generateText(options){
 if(options.model.modelId!==process.env.AO_TEST_MASTRA_MODEL||options.tools||options.maxRetries!==0||!options.abortSignal||options.maxOutputTokens>32||!options.prompt)throw Error('invalid provider verification options');
 if(!process.env.ZHIPU_API_KEY&&!process.env.MINIMAX_API_KEY&&!['oauth','custom'].includes(process.env.AO_TEST_MASTRA_CREDENTIAL))throw Error('credential resolution missing');
 if(process.env.AO_TEST_MASTRA_OUTCOME==='transport')throw Error('fixture-secret raw transport');
 if(process.env.AO_TEST_MASTRA_OUTCOME==='malformed')return {};
 if(!['success','reasoning'].includes(process.env.AO_TEST_MASTRA_OUTCOME)){const err=Error('fixture-secret raw provider');err.statusCode=process.env.AO_TEST_MASTRA_OUTCOME==='quota403'?403:Number(process.env.AO_TEST_MASTRA_OUTCOME);if(process.env.AO_TEST_MASTRA_OUTCOME==='quota403')err.data={error:{code:'insufficient_quota'}};throw err}
 return {text:process.env.AO_TEST_MASTRA_OUTCOME==='reasoning'?'':'fixture-secret provider output',usage:{inputTokens:1,outputTokens:1,totalTokens:2},response:{modelId:options.model.modelId},finishReason:process.env.AO_TEST_MASTRA_OUTCOME==='reasoning'?'length':'stop'};
 }`)
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "node_modules/mastracode/dist/cli.js"), filepath.Join(bin, "mastracode")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("AO_DATA_DIR", t.TempDir())
	t.Setenv("MASTRA_APP_DATA_DIR", "")
	t.Setenv("MASTRACODE_MODEL_ID", "")
	t.Setenv("ZHIPU_API_KEY", "")
	t.Setenv("MINIMAX_API_KEY", "")
	t.Setenv("AO_TEST_MASTRA_CREDENTIAL", credential)
	t.Setenv("AO_TEST_MASTRA_MODEL", model)
	t.Setenv("AO_TEST_MASTRA_OUTCOME", outcome)
	if credential == "env" {
		t.Setenv("MINIMAX_API_KEY", "fixture-secret")
	}
	return root
}

func TestNativeAuthUsesSelectedProviderAndVerifiedResult(t *testing.T) {
	for _, tc := range []struct {
		name, credential, model, outcome string
		want                             ports.AgentAuthStatus
	}{
		{"stored key", "stored", "zai/glm-test", "success", ports.AgentAuthStatusAuthorized},
		{"reasoning length limit", "stored", "zai/glm-test", "reasoning", ports.AgentAuthStatusAuthorized},
		{"malformed result", "stored", "zai/glm-test", "malformed", ports.AgentAuthStatusConfigured},
		{"native environment", "env", "minimax/model-test", "success", ports.AgentAuthStatusAuthorized},
		{"OAuth", "oauth", "zai/glm-test", "success", ports.AgentAuthStatusAuthorized},
		{"custom provider", "custom", "local/test", "success", ports.AgentAuthStatusAuthorized},
		{"key without model", "stored", "", "success", ports.AgentAuthStatusConfigured},
		{"no credential", "", "", "success", ports.AgentAuthStatusUnknown},
		{"rejected", "stored", "zai/glm-test", "401", ports.AgentAuthStatusUnauthorized},
		{"forbidden", "stored", "zai/glm-test", "403", ports.AgentAuthStatusUnauthorized},
		{"rate limit", "stored", "zai/glm-test", "429", ports.AgentAuthStatusConfigured},
		{"quota", "stored", "zai/glm-test", "402", ports.AgentAuthStatusConfigured},
		{"transport", "stored", "zai/glm-test", "transport", ports.AgentAuthStatusConfigured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixtureNativeAuth(t, tc.credential, tc.model, tc.outcome)
			got, err := New().AuthStatus(t.Context())
			if err != nil || got != tc.want {
				t.Fatalf("AuthStatus = %s, %v; want %s", got, err, tc.want)
			}
		})
	}
}

func TestNativeAuthDiagnosticsNeverExposeProviderOutput(t *testing.T) {
	for _, tc := range []struct{ outcome, code string }{
		{"success", "provider_accepted"}, {"401", "provider_rejected"}, {"429", "provider_rate_limited"}, {"402", "provider_quota_exhausted"}, {"quota403", "provider_quota_exhausted"}, {"transport", "provider_unreachable"},
	} {
		t.Run(tc.outcome, func(t *testing.T) {
			fixtureNativeAuth(t, "stored", "zai/glm-test", tc.outcome)
			status, identity, steps, err := New().AuthStatusWithDiagnostics(t.Context())
			if err != nil || len(steps) != 1 || steps[0].Code != tc.code || identity.Provider != "zai" {
				t.Fatalf("diagnostics = %s, %#v, %#v, %v", status, identity, steps, err)
			}
			safe, _ := json.Marshal(struct {
				Status      ports.AgentAuthStatus
				Identity    any
				Diagnostics any
			}{status, identity, steps})
			for _, forbidden := range []string{"fixture-secret", "raw provider", "raw transport", os.Getenv("AO_DATA_DIR")} {
				if strings.Contains(string(safe), forbidden) {
					t.Fatalf("unsafe auth result: %s", safe)
				}
			}
		})
	}
}

func TestMastraDeclinesUnqualifiedTerminalRefresh(t *testing.T) {
	if New().SupportsTerminalVersionRefresh() {
		t.Fatal("unqualified unattended refresh advertised")
	}
}

func TestNativeAuthIncompatibleSDKIsInconclusive(t *testing.T) {
	root := fixtureNativeAuth(t, "stored", "zai/glm-test", "success")
	path := filepath.Join(root, "node_modules/@mastra/code-sdk/package.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(strings.Replace(string(data), "1.11.0", "9.0.0", 1)), 0600); err != nil {
		t.Fatal(err)
	}
	status, _, steps, err := New().AuthStatusWithDiagnostics(t.Context())
	if err != nil || status != ports.AgentAuthStatusUnknown || len(steps) != 1 || steps[0].Code != "mastra_sdk_unsupported" {
		t.Fatalf("incompatible SDK = %s, %#v, %v", status, steps, err)
	}
}

func TestNativeAuthCreatesNoSessionFiles(t *testing.T) {
	fixtureNativeAuth(t, "stored", "zai/glm-test", "success")
	if status, err := New().AuthStatus(t.Context()); err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("probe = %s, %v", status, err)
	}
	files, err := os.ReadDir(os.Getenv("AO_DATA_DIR"))
	if err != nil || len(files) != 0 {
		t.Fatalf("probe created persistent state: %v, %v", files, err)
	}
}

func TestNativeAuthCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	status, _, steps, err := New().AuthStatusWithDiagnostics(ctx)
	if status != ports.AgentAuthStatusUnknown || !errors.Is(err, context.Canceled) || len(steps) != 1 || steps[0].Code != "probe_cancelled" {
		t.Fatalf("cancelled = %s, %#v, %v", status, steps, err)
	}
}

func TestNativeAuthWithoutSelectedModelHasActionableDiagnostic(t *testing.T) {
	fixtureNativeAuth(t, "stored", "", "success")
	status, _, steps, err := New().AuthStatusWithDiagnostics(t.Context())
	if err != nil || status != ports.AgentAuthStatusConfigured || len(steps) != 1 || steps[0].Code != "mastra_model_unselected" || steps[0].NextStep == "" {
		t.Fatalf("no selected model = %s, %#v, %v", status, steps, err)
	}
}

func TestNativeAuthResolvesNpmExecutableShim(t *testing.T) {
	root := fixtureNativeAuth(t, "stored", "zai/glm-test", "success")
	// npm on Windows installs a regular command shim alongside node_modules,
	// rather than a symlink into the package. The same layout is reproducible here.
	if err := os.WriteFile(filepath.Join(root, "mastracode"), []byte("#!/bin/sh\nexit 0\n"), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", root+string(os.PathListSeparator)+os.Getenv("PATH"))
	status, err := New().AuthStatus(t.Context())
	if err != nil || status != ports.AgentAuthStatusAuthorized {
		t.Fatalf("npm shim = %s, %v", status, err)
	}
}

func TestNativeAuthIgnoresModelEnvironmentReplacedByLaunch(t *testing.T) {
	fixtureNativeAuth(t, "stored", "", "success")
	t.Setenv("MASTRACODE_MODEL_ID", "zai/glm-test")
	status, _, steps, err := New().AuthStatusWithDiagnostics(t.Context())
	if err != nil || status != ports.AgentAuthStatusConfigured || len(steps) != 1 || steps[0].Code != "mastra_model_unselected" {
		t.Fatalf("inherited model should not be probed = %s, %#v, %v", status, steps, err)
	}
}
