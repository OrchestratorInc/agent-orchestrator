package openhands

// AugmentRuntimeEnv isolates OpenHands' LiteLLM configuration from an ambient
// OpenAI SDK endpoint. Its explicit LLM_BASE_URL remains authoritative. Pin
// the SDK fallback rather than blanking it: the SDK treats an empty URL as
// an explicit value instead of falling back to its public endpoint.
// LLM_* values already belong to the provider process environment; inherited
// credentials must not be materialized in argv or the runtime's override map,
// which can be serialized while the terminal is starting.
func (p *Plugin) AugmentRuntimeEnv(env map[string]string, _ string) {
	env["OPENAI_BASE_URL"] = "https://api.openai.com/v1"
}
