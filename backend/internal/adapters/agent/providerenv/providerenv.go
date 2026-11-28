// Package providerenv preserves explicit provider environment overrides across
// the terminal login shell without placing endpoints or credentials in argv.
package providerenv

import (
	"runtime"
	"strings"

	"github.com/OrchestratorInc/agent-orchestrator/backend/internal/agentlaunch"
)

// commandGOOS selects the platform for Command; tests override it.
var commandGOOS = runtime.GOOS

const bridgePrefix = "AO_EXPLICIT_PROVIDER_"

// Supported environment names from CodeWhale v0.10.2's
// crates/config/assets/provider_descriptors.json and docs/PROVIDERS.md,
// docs/CONFIGURATION.md, and Prime Agent c83ac68b's
// crates/pa-ai/src/env_api_keys.rs. This table contains names only: native
// defaults, selected providers and credential discovery remain harness-owned.
var variables = []string{
	"AI_GATEWAY_API_KEY",
	"ANTHROPIC_API_KEY",
	"ANTHROPIC_AUTH_TOKEN",
	"ANTHROPIC_BASE_URL",
	"ANTHROPIC_OAUTH_TOKEN",
	"ARCEE_API_KEY",
	"ARCEE_BASE_URL",
	"ARK_API_KEY",
	"ARK_BASE_URL",
	"ATLASCLOUD_API_KEY",
	"ATLASCLOUD_BASE_URL",
	"AWS_ACCESS_KEY_ID",
	"AWS_BEARER_TOKEN_BEDROCK",
	"AWS_CONTAINER_CREDENTIALS_FULL_URI",
	"AWS_CONTAINER_CREDENTIALS_RELATIVE_URI",
	"AWS_PROFILE",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_WEB_IDENTITY_TOKEN_FILE",
	"AZURE_OPENAI_API_KEY",
	"BAIDU_QIANFAN_API_KEY",
	"BAIDU_QIANFAN_BASE_URL",
	"CEREBRAS_API_KEY",
	"CLOUDFLARE_API_KEY",
	"CODEWHALE_API_KEY",
	"CODEWHALE_BASE_URL",
	"CODEX_ACCESS_TOKEN",
	"CODEX_BASE_URL",
	"CONCENTRATE_API_KEY",
	"CONCENTRATE_BASE_URL",
	"COPILOT_GITHUB_TOKEN",
	"CSDN_API_KEY",
	"CSDN_BASE_URL",
	"DASHSCOPE_API_KEY",
	"DEEPINFRA_API_KEY",
	"DEEPINFRA_BASE_URL",
	"DEEPINFRA_TOKEN",
	"DEEPSEEK_ANTHROPIC_BASE_URL",
	"DEEPSEEK_API_KEY",
	"DEEPSEEK_BASE_URL",
	"DEEPSEEK_HTTP_HEADERS",
	"EDENAI_API_KEY",
	"EDENAI_BASE_URL",
	"FIREWORKS_API_KEY",
	"FIREWORKS_BASE_URL",
	"FUGU_API_KEY",
	"GCLOUD_PROJECT",
	"GEMINI_API_KEY",
	"GEMINI_BASE_URL",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GLM_API_KEY",
	"GOOGLE_API_KEY",
	"GOOGLE_APPLICATION_CREDENTIALS",
	"GOOGLE_BASE_URL",
	"GOOGLE_CLOUD_API_KEY",
	"GOOGLE_CLOUD_LOCATION",
	"GOOGLE_CLOUD_PROJECT",
	"GROQ_API_KEY",
	"HF_BASE_URL",
	"HF_TOKEN",
	"HUGGINGFACE_API_KEY",
	"HUGGINGFACE_BASE_URL",
	"KIMI_API_KEY",
	"KIMI_BASE_URL",
	"LONGCAT_API_KEY",
	"LONGCAT_BASE_URL",
	"META_MODEL_API_BASE_URL",
	"META_MODEL_API_KEY",
	"MIMO_API_KEY",
	"MIMO_BASE_URL",
	"MIMO_TOKEN_PLAN_API_KEY",
	"MINIMAX_ANTHROPIC_BASE_URL",
	"MINIMAX_API_KEY",
	"MINIMAX_BASE_URL",
	"MINIMAX_CN_API_KEY",
	"MISTRAL_API_KEY",
	"MISTRAL_BASE_URL",
	"MODELSCOPE_API_KEY",
	"MODELSCOPE_BASE_URL",
	"MODELSTUDIO_API_KEY",
	"MODELSTUDIO_CODING_PLAN_BASE_URL",
	"MODELSTUDIO_TOKEN_PLAN_BASE_URL",
	"MODEL_API_BASE_URL",
	"MODEL_API_KEY",
	"MOONSHOT_API_KEY",
	"MOONSHOT_BASE_URL",
	"NIM_BASE_URL",
	"NOVITA_API_KEY",
	"NOVITA_BASE_URL",
	"NVIDIA_API_KEY",
	"NVIDIA_BASE_URL",
	"NVIDIA_NIM_API_KEY",
	"NVIDIA_NIM_BASE_URL",
	"OLLAMA_API_KEY",
	"OLLAMA_BASE_URL",
	"OLLAMA_CLOUD_API_KEY",
	"OLLAMA_CLOUD_BASE_URL",
	"OPENAI_API_KEY",
	"OPENAI_BASE_URL",
	"OPENAI_CODEX_ACCESS_TOKEN",
	"OPENAI_CODEX_BASE_URL",
	"OPENCODE_API_KEY",
	"OPENCODE_GO_API_KEY",
	"OPENCODE_GO_BASE_URL",
	"OPENCODE_ZEN_API_KEY",
	"OPENCODE_ZEN_BASE_URL",
	"OPENMODEL_API_KEY",
	"OPENMODEL_BASE_URL",
	"OPENROUTER_API_KEY",
	"OPENROUTER_BASE_URL",
	"ORCAROUTER_API_KEY",
	"ORCAROUTER_AUTH_BASE_URL",
	"ORCAROUTER_BASE_URL",
	"ORCA_API_BASE_URL",
	"ORCA_AUTH_BASE_URL",
	"ORCA_BASE_URL",
	"PRIME_API_KEY",
	"QIANFAN_API_KEY",
	"QIANFAN_BASE_URL",
	"SAKANA_API_KEY",
	"SAKANA_BASE_URL",
	"SGLANG_API_KEY",
	"SGLANG_BASE_URL",
	"SILICONFLOW_API_KEY",
	"SILICONFLOW_BASE_URL",
	"STEPFUN_API_KEY",
	"STEPFUN_BASE_URL",
	"STEP_API_KEY",
	"STEP_BASE_URL",
	"SUPERFAST_BASE_URL",
	"TELECOMJS_API_KEY",
	"TELECOMJS_BASE_URL",
	"TOGETHER_API_KEY",
	"TOGETHER_BASE_URL",
	"VLLM_API_KEY",
	"VLLM_BASE_URL",
	"VOLCENGINE_API_KEY",
	"VOLCENGINE_ARK_API_KEY",
	"VOLCENGINE_ARK_BASE_URL",
	"VOLCENGINE_BASE_URL",
	"WANJIE_API_KEY",
	"WANJIE_ARK_API_KEY",
	"WANJIE_ARK_BASE_URL",
	"WANJIE_BASE_URL",
	"WANJIE_MAAS_API_KEY",
	"WANJIE_MAAS_BASE_URL",
	"XAI_API_KEY",
	"XAI_BASE_URL",
	"XIAOMI_API_KEY",
	"XIAOMI_MIMO_API_KEY",
	"XIAOMI_MIMO_BASE_URL",
	"XIAOMI_MIMO_TOKEN_PLAN_API_KEY",
	"XIAOMI_TOKEN_PLAN_AMS_API_KEY",
	"XIAOMI_TOKEN_PLAN_CN_API_KEY",
	"XIAOMI_TOKEN_PLAN_SGP_API_KEY",
	"ZAI_API_KEY",
	"ZAI_BASE_URL",
	"ZENMUX_API_KEY",
	"ZENMUX_BASE_URL",
	"ZHIPU_API_KEY",
	"Z_AI_API_KEY",
	"Z_AI_BASE_URL",
}

// Capture runs through the adapters' existing AugmentRuntimeEnv hook, before
// the login shell. Values travel in the runtime's private ephemeral launch
// environment, never in command arguments or durable adapter configuration.
func Capture(env map[string]string) {
	if env == nil {
		return
	}
	for _, key := range variables {
		bridge := bridgePrefix + key
		delete(env, bridge)
		if value, explicit := env[key]; explicit && value != "" {
			env[bridge] = value
		}
	}
}

// Command reapplies only explicit AO/project values after shell startup.
// Absent values leave shell/native routing untouched; empty values clear it.
// The script contains allowlisted names only, including for credentials and
// URLs that may embed credentials. Shell expansions are quoted and not eval'd.
//
// Windows ConPTY applies the launch env map directly with no login shell to
// override it, and has no sh, so argv is returned unchanged there.
func Command(argv []string, env map[string]string) []string {
	if commandGOOS == "windows" {
		return argv
	}
	var script strings.Builder
	for _, key := range variables {
		value, explicit := env[key]
		if !explicit {
			continue
		}
		bridge := bridgePrefix + key
		if value == "" {
			script.WriteString("unset " + key + "; ")
		} else {
			script.WriteString("if [ \"${" + bridge + "+set}\" != set ]; then echo 'provider launch environment is missing' >&2; exit 1; fi; ")
			script.WriteString("export " + key + "=\"$" + bridge + "\"; ")
		}
		script.WriteString("unset " + bridge + "; ")
	}
	if script.Len() == 0 {
		return argv
	}
	script.WriteString("exec \"$@\"")
	return append([]string{"sh", "-c", script.String(), agentlaunch.ProviderEnvMarker}, argv...)
}
