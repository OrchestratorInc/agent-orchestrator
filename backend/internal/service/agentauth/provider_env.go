package agentauth

import (
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/providerkeys"
)

var publishProviderKey = providerkeys.Publish

// Environment methods are deliberately per harness. A key is offered only
// where that CLI has a documented environment credential source. The same
// provider key is device-wide for AO-launched sessions, regardless of project.
var providerEnvironment = map[string][]LoginMethod{
	"claude-code":  {{"env_ANTHROPIC_API_KEY", "Anthropic API key", true}},
	"aider":        {{"env_GROQ_API_KEY", "Groq API key", true}, {"env_XAI_API_KEY", "xAI API key", true}, {"env_MISTRAL_API_KEY", "Mistral API key", true}},
	"opencode":     {{"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}, {"env_GROQ_API_KEY", "Groq API key", true}, {"env_XAI_API_KEY", "xAI API key", true}, {"env_MISTRAL_API_KEY", "Mistral API key", true}},
	"opencode-v2":  {{"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}, {"env_GROQ_API_KEY", "Groq API key", true}, {"env_XAI_API_KEY", "xAI API key", true}, {"env_MISTRAL_API_KEY", "Mistral API key", true}},
	"grok":         {{"env_XAI_API_KEY", "xAI API key", true}},
	"droid":        {{"env_FACTORY_API_KEY", "Factory API key", true}},
	"amp":          {{"env_AMP_API_KEY", "Amp API key", true}},
	"crush":        {{"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}},
	"cursor":       {{"env_CURSOR_API_KEY", "Cursor API key", true}},
	"qwen":         {{"env_DASHSCOPE_API_KEY", "DashScope API key (China endpoint)", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}},
	"goose":        {{"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}},
	"continue":     {{"env_CONTINUE_API_KEY", "Continue API key", true}, {"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}},
	"devin":        {{"env_DEVIN_API_KEY", "Devin API key", true}},
	"cline":        {{"env_CLINE_API_KEY", "Cline API key", true}},
	"kimi":         {{"env_KIMI_API_KEY", "Kimi API key", true}, {"env_MOONSHOT_API_KEY", "Moonshot API key", true}},
	"muse":         {{"env_META_API_KEY", "Meta API key", true}},
	"kiro":         {{"env_KIRO_API_KEY", "Kiro API key", true}},
	"kilocode":     {{"env_KILO_API_KEY", "Kilo API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}},
	"vibe":         {{"env_MISTRAL_API_KEY", "Mistral API key", true}},
	"pi":           {{"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}},
	"kimchi":       {{"env_KIMCHI_API_KEY", "Kimchi API key", true}},
	"prime-agent":  {{"env_PRIME_API_KEY", "Prime API key", true}, {"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}},
	"omp":          {{"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}},
	"fx":           {{"env_AI_GATEWAY_API_KEY", "Vercel AI Gateway API key", true}},
	"autohand":     {{"env_AUTOHAND_API_KEY", "Autohand API key", true}, {"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}},
	"unreal-agent": {{"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_FIREWORKS_API_KEY", "Fireworks API key", true}},
	"memcode":      {{"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_ANTHROPIC_API_KEY", "Anthropic API key", true}},
	"command-code": {{"env_COMMAND_CODE_API_KEY", "Command Code API key", true}},
	"hermes":       {{"env_ANTHROPIC_API_KEY", "Anthropic API key", true}, {"env_OPENAI_API_KEY", "OpenAI API key", true}, {"env_GEMINI_API_KEY", "Gemini API key", true}, {"env_OPENROUTER_API_KEY", "OpenRouter API key", true}, {"env_DEEPSEEK_API_KEY", "DeepSeek API key", true}},
	"openhands":    {{"env_LLM_API_KEY", "OpenHands LLM API key", true}},
}

func environmentMethod(agentID, method string) (string, bool) {
	for _, candidate := range providerEnvironment[agentID] {
		if candidate.ID == method {
			return strings.TrimPrefix(method, "env_"), true
		}
	}
	return "", false
}
