package modelcatalog

// Code generated from the built-in provider registry of @neovate/code v0.28.5
// (dist/cli.mjs). Regenerate when the pinned Neovate version changes.

// neovateBuiltInProviders lists Neovate's built-in providers in its own order,
// with the key variables it reads and every model it offers. Providers without
// variables (Codex, Qwen) authenticate only through native /login.
var neovateBuiltInProviders = []struct {
	id     string
	env    []string
	models []string
}{
	{"openai", []string{"OPENAI_API_KEY"}, []string{"gpt-4.1", "gpt-4", "gpt-4o", "o3", "o3-mini", "o4-mini", "gpt-5.1", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex-max", "gpt-5", "gpt-5-mini", "gpt-5-codex", "gpt-5.2", "gpt-5.2-pro", "gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.3-codex-spark"}},
	{"google", []string{"GOOGLE_API_KEY", "GOOGLE_GENERATIVE_AI_API_KEY"}, []string{"gemini-2.5-flash", "gemini-2.5-flash-preview-09-2025", "gemini-2.5-flash-lite", "gemini-2.5-pro", "gemini-3-pro-preview", "gemini-3-flash-preview"}},
	{"deepseek", []string{"DEEPSEEK_API_KEY"}, []string{"deepseek-chat", "deepseek-reasoner"}},
	{"xai", []string{"XAI_API_KEY"}, []string{"grok-4-1-fast", "grok-4-1-fast-non-reasoning", "grok-4", "grok-4-fast", "grok-code-fast-1"}},
	{"anthropic", []string{"ANTHROPIC_API_KEY"}, []string{"claude-opus-4-20250514", "claude-opus-4-1-20250805", "claude-sonnet-4-20250514", "claude-sonnet-4-5-20250929", "claude-3-7-sonnet-20250219", "claude-3-7-sonnet-20250219-thinking", "claude-3-5-sonnet-20241022", "claude-haiku-4-5", "claude-opus-4-5", "claude-sonnet-4-6", "claude-opus-4-6"}},
	{"aihubmix", []string{"AIHUBMIX_API_KEY"}, []string{"gemini-2.5-pro", "gemini-2.5-flash", "gemini-2.5-flash-lite", "DeepSeek-R1", "DeepSeek-V3", "claude-opus-4-20250514", "claude-opus-4-1", "claude-sonnet-4-20250514", "claude-sonnet-4-5", "claude-3-7-sonnet-20250219", "claude-3-5-sonnet-20241022", "gpt-4.1", "gpt-4", "gpt-4o", "o3", "o3-mini", "o4-mini", "gpt-5", "gpt-5-mini", "glm-4.6", "kimi-k2-thinking", "kimi-k2-turbo-preview"}},
	{"openrouter", []string{"OPENROUTER_API_KEY", "OPEN_ROUTER_API_KEY"}, []string{"anthropic/claude-3.5-sonnet", "anthropic/claude-3.7-sonnet", "anthropic/claude-sonnet-4", "anthropic/claude-sonnet-4.5", "anthropic/claude-haiku-4.5", "anthropic/claude-opus-4", "anthropic/claude-opus-4.1", "anthropic/claude-opus-4.5", "anthropic/claude-sonnet-4.6", "anthropic/claude-opus-4.6", "deepseek/deepseek-r1-0528", "deepseek/deepseek-chat-v3-0324", "deepseek/deepseek-chat-v3.1", "deepseek/deepseek-v3.1-terminus", "deepseek/deepseek-v3.2-exp", "deepseek/deepseek-v3.2", "deepseek/deepseek-v3.2-speciale", "openai/gpt-4.1", "openai/gpt-4", "openai/gpt-4o", "openai/o3", "openai/o3-pro", "openai/o3-mini", "openai/o4-mini", "openai/gpt-oss-120b", "openai/gpt-5.1-codex", "openai/gpt-5.1-codex-mini", "openai/gpt-5.1-codex-max", "openai/gpt-5.1", "openai/gpt-5", "openai/gpt-5-mini", "openai/gpt-5-codex", "openai/gpt-5.2", "openai/gpt-5.2-pro", "openai/gpt-5.2-codex", "google/gemini-3-flash-preview", "google/gemini-3-pro-preview", "moonshotai/kimi-k2", "moonshotai/kimi-k2-0905", "moonshotai/kimi-k2-thinking", "moonshotai/kimi-k2.5", "qwen/qwen3-coder", "qwen/qwen3-max", "x-ai/grok-code-fast-1", "x-ai/grok-4", "x-ai/grok-4-fast", "x-ai/grok-4.1-fast", "z-ai/glm-4.5", "z-ai/glm-4.5v", "z-ai/glm-4.6", "z-ai/glm-4.6v", "z-ai/glm-4.7", "z-ai/glm-5", "minimax/minimax-m2", "minimax/minimax-m2.5"}},
	{"moonshotai", []string{"MOONSHOT_API_KEY"}, []string{"kimi-k2-0905-preview", "kimi-k2-turbo-preview", "kimi-k2-thinking", "kimi-k2-thinking-turbo", "kimi-k2.5"}},
	{"moonshotai-cn", []string{"MOONSHOT_API_KEY"}, []string{"kimi-k2-0905-preview", "kimi-k2-turbo-preview", "kimi-k2-thinking", "kimi-k2-thinking-turbo", "kimi-k2.5"}},
	{"groq", []string{"GROQ_API_KEY"}, []string{"openai/gpt-oss-120b", "moonshotai/kimi-k2-instruct-0905"}},
	{"siliconflow", []string{"SILICONFLOW_API_KEY"}, []string{"Qwen/Qwen3-235B-A22B-Instruct-2507", "Qwen/Qwen3-Coder-480B-A35B-Instruct", "moonshotai/Kimi-K2-Instruct-0905", "moonshotai/Kimi-K2-Instruct", "deepseek-ai/DeepSeek-R1", "deepseek-ai/DeepSeek-V3.1", "deepseek-ai/DeepSeek-V3", "zai-org/GLM-4.5"}},
	{"siliconflow-cn", []string{"SILICONFLOW_API_KEY"}, []string{"Qwen/Qwen3-235B-A22B-Instruct-2507", "Qwen/Qwen3-Coder-480B-A35B-Instruct", "moonshotai/Kimi-K2-Instruct-0905", "moonshotai/Kimi-K2-Instruct", "deepseek-ai/DeepSeek-R1", "deepseek-ai/DeepSeek-V3.1", "deepseek-ai/DeepSeek-V3", "zai-org/GLM-4.5", "Pro/moonshotai/Kimi-K2.5", "Pro/zai-org/GLM-5", "Pro/zai-org/GLM-4.7", "Pro/MiniMaxAI/MiniMax-M2.5", "Pro/deepseek-ai/DeepSeek-V3.2"}},
	{"modelscope", []string{"MODELSCOPE_API_KEY"}, []string{"Qwen/Qwen3-Coder-480B-A35B-Instruct", "Qwen/Qwen3-235B-A22B-Instruct-2507", "ZhipuAI/GLM-4.5", "ZhipuAI/GLM-4.5V", "ZhipuAI/GLM-4.6", "ZhipuAI/GLM-5", "deepseek-ai/DeepSeek-V3.2", "deepseek-ai/DeepSeek-V3.2-Speciale"}},
	{"volcengine", []string{"VOLCENGINE_API_KEY"}, []string{"deepseek-v3-1-250821", "deepseek-v3-1-terminus", "doubao-seed-1-6-250615", "kimi-k2-250905"}},
	{"zai-coding-plan", []string{"ZHIPU_API_KEY"}, []string{"glm-4.5-flash", "glm-4.5", "glm-4.5-air", "glm-4.5v", "glm-4.6", "glm-4.6v", "glm-4.7", "glm-5"}},
	{"zhipuai-coding-plan", []string{"ZHIPU_API_KEY"}, []string{"glm-4.5v", "glm-4.5-air", "glm-4.5", "glm-4.5-flash", "glm-4.6", "glm-4.6v", "glm-4.7", "glm-5"}},
	{"zhipuai", []string{"ZHIPU_API_KEY"}, []string{"glm-4.6", "glm-4.5v", "glm-4.5-air", "glm-4.5", "glm-4.5-flash", "glm-4.6v", "glm-4.7"}},
	{"zenmux", []string{"ZENMUX_API_KEY"}, []string{"google/gemini-3-flash-preview", "google/gemini-3-pro-preview", "openai/gpt-5.1", "openai/gpt-5.1-codex", "openai/gpt-5.1-codex-mini", "openai/gpt-5.2", "openai/gpt-5.2-pro", "anthropic/claude-sonnet-4.5", "anthropic/claude-opus-4.5", "anthropic/claude-sonnet-4.6", "anthropic/claude-opus-4.6", "z-ai/glm-4.6", "z-ai/glm-4.6v", "z-ai/glm-4.7", "z-ai/glm-5", "deepseek/deepseek-chat", "deepseek/deepseek-reasoner", "moonshotai/kimi-k2.5", "minimax/minimax-m2.5"}},
	{"minimax", []string{"MINIMAX_API_KEY"}, []string{"minimax-m2", "minimax-m2.1", "minimax-m2.5"}},
	{"minimax-cn", []string{"MINIMAX_API_KEY"}, []string{"minimax-m2", "minimax-m2.1", "minimax-m2.5"}},
	{"xiaomi", []string{"MIMO_API_KEY"}, []string{"mimo-v2-flash"}},
	{"cerebras", []string{"CEREBRAS_API_KEY"}, []string{"zai-glm-4.7", "gpt-oss-120b"}},
	{"huggingface", []string{"HUGGINGFACE_API_KEY"}, []string{"zai-org/GLM-4.7", "XiaomiMiMo/MiMo-V2-Flash", "Qwen/Qwen3-Coder-480B-A35B-Instruct", "moonshotai/Kimi-K2.5"}},
	{"poe", []string{"POE_API_KEY"}, []string{"Claude-Opus-4.5", "Claude-Opus-4.6", "Claude-Sonnet-4.5", "Gemini-3-Pro", "Gemini-2.5-Pro", "Gemini-2.5-Flash", "GPT-5.1", "GPT-5.1-Codex", "Grok-4.1-Fast"}},
	{"nvidia", []string{"NVIDIA_API_KEY"}, []string{"z-ai/glm4.7", "minimaxai/minimax-m2.1", "moonshotai/kimi-k2-thinking", "moonshotai/kimi-k2.5", "openai/gpt-oss-120b", "qwen/qwen3-coder-480b-a35b-instruct"}},
	{"canopywave", []string{"CANOPYWAVE_API_KEY"}, []string{"minimax/minimax-m2.1", "minimax/minimax-m2.5", "zai/glm-4.7", "zai/glm-5", "moonshotai/kimi-k2-thinking", "moonshotai/kimi-k2.5", "deepseek/deepseek-chat-v3.2", "openai/gpt-oss-120b", "xiaomimimo/mimo-v2-flash"}},
	{"modelwatch", []string{"MODELWATCH_API_KEY"}, []string{"qwen3-coder-plus", "glm-4.7", "gemini-2.5-flash", "gemini-3-flash", "gemini-3-pro-preview", "claude-4-5-sonnet", "claude-4-6-sonnet", "claude-haiku-4-5", "claude-opus-4-5", "claude-opus-4-6", "gpt-5.1", "gpt-5.1-codex-max", "gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.2", "gpt-5.2-codex"}},
	{"opencode", []string{"OPENCODE_API_KEY"}, []string{"glm-4.7", "glm-4.7-free", "glm-4.6", "qwen3-coder", "claude-4.1-opus", "kimi-k2", "gpt-5.2-codex", "gpt-5.1-codex", "claude-haiku-4-5", "claude-opus-4-5", "kimi-k2.5", "kimi-k2.5-free", "gemini-3-pro", "claude-4-5-sonnet", "gpt-5.1-codex-mini", "kimi-k2-thinking", "gpt-5.1", "gpt-5-codex", "gemini-3-flash", "gpt-5.1-codex-max", "minimax-m2.1-free", "claude-4-sonnet", "gpt-5", "minimax-m2.1", "gpt-5.2"}},
	{"qwen", nil, []string{"coder-model"}},
	{"codex", nil, []string{"gpt-5.1-codex", "gpt-5.1-codex-mini", "gpt-5.1-codex-max", "gpt-5.2", "gpt-5.2-codex", "gpt-5.3-codex", "gpt-5.3-codex-spark"}},
	{"kilo", []string{"KILO_API_KEY"}, []string{"z-ai/glm-5", "z-ai/glm-5:free", "z-ai/glm-4.7", "anthropic/claude-opus-4.6", "anthropic/claude-sonnet-4.6", "anthropic/claude-haiku-4.5", "anthropic/claude-sonnet-4.5", "google/gemini-3-flash-preview", "google/gemini-3-pro-preview", "minimax/minimax-m2.5:free", "minimax/minimax-m2.5", "moonshotai/kimi-k2.5"}},
	{"bailian-coding-plan", []string{"BAILIAN_CODING_API_KEY"}, []string{"qwen3.5-plus", "qwen3-max-2026-01-23", "qwen3-coder-next", "qwen3-coder-plus", "MiniMax-M2.5", "glm-5", "glm-4.7", "kimi-k2.5"}},
	{"kimi-coding-plan", []string{"KIMI_CODING_API_KEY"}, []string{"kimi-for-coding"}},
}
