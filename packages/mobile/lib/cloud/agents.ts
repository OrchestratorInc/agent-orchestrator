import type { CloudProviderConnection } from "./types";

/** The coding agents Orchestrator Cloud runs, in the order the desktop lists them. */
export const CLOUD_AGENTS = ["claude-code", "codex", "cursor", "opencode"] as const;
export type CloudAgent = (typeof CLOUD_AGENTS)[number];

export function isCloudAgent(value: string): value is CloudAgent {
	return (CLOUD_AGENTS as readonly string[]).includes(value);
}

export const CLOUD_AGENT_LABELS: Record<CloudAgent, string> = {
	"claude-code": "Claude Code",
	codex: "Codex",
	cursor: "Cursor",
	opencode: "OpenCode",
};

export function cloudAgentLabel(agent: string): string {
	return isCloudAgent(agent) ? CLOUD_AGENT_LABELS[agent] : agent;
}

/**
 * One way to give Orchestrator Cloud a credential for an agent.
 *
 * `login` methods run an interactive sign-in on the phone and upload what it
 * produces; `paste` methods take a secret the person already has. The
 * credential types are the control plane's (harness_spec.go): it validates each
 * one against the provider before storing it.
 */
export type CloudCredentialMethod = {
	id: string;
	credentialType: string;
	kind: "login" | "paste";
	title: string;
	description: string;
	/** Placeholder for the paste field. */
	placeholder?: string;
	/** A multi-line document rather than a one-line token. */
	multiline?: boolean;
};

export const CLOUD_CREDENTIAL_METHODS: Record<CloudAgent, readonly CloudCredentialMethod[]> = {
	"claude-code": [
		{ id: "claude-login", credentialType: "oauth_token", kind: "login", title: "Sign in with Claude", description: "Use your Claude Pro or Max plan. Opens claude.com in the browser you're already signed in to; approve and you're back." },
		{ id: "claude-setup-token", credentialType: "oauth_token", kind: "paste", title: "Paste a setup token", description: "Run `claude setup-token` on any computer and paste the token (sk-ant-oat01-…).", placeholder: "sk-ant-oat01-…" },
		{ id: "anthropic-api-key", credentialType: "api_key", kind: "paste", title: "Anthropic API key", description: "Bill usage to an Anthropic Console API key.", placeholder: "sk-ant-api03-…" },
	],
	codex: [
		{ id: "codex-login", credentialType: "auth_json", kind: "login", title: "Sign in with ChatGPT", description: "Use your ChatGPT plan. Opens ChatGPT in the browser you're already signed in to; approve and you're back." },
		{ id: "openai-api-key", credentialType: "api_key", kind: "paste", title: "OpenAI API key", description: "Bill usage to an OpenAI Platform API key.", placeholder: "sk-…" },
		{ id: "codex-auth-json", credentialType: "auth_json", kind: "paste", title: "Paste auth.json", description: "The contents of ~/.codex/auth.json from a computer where Codex is signed in.", placeholder: "{ \"tokens\": … }", multiline: true },
	],
	cursor: [
		{ id: "cursor-api-key", credentialType: "api_key", kind: "paste", title: "Cursor API key", description: "Create one in Cursor → Settings → Integrations.", placeholder: "key_…" },
	],
	opencode: [
		{ id: "opencode-api-key", credentialType: "opencode_api_key", kind: "paste", title: "OpenCode Zen key", description: "An OpenCode Zen API key.", placeholder: "sk-…" },
		{ id: "opencode-anthropic", credentialType: "anthropic_api_key", kind: "paste", title: "Anthropic API key", description: "OpenCode runs Claude models with this key.", placeholder: "sk-ant-api03-…" },
		{ id: "opencode-openai", credentialType: "openai_api_key", kind: "paste", title: "OpenAI API key", description: "OpenCode runs OpenAI models with this key.", placeholder: "sk-…" },
		{ id: "opencode-openrouter", credentialType: "openrouter_api_key", kind: "paste", title: "OpenRouter API key", description: "OpenCode runs OpenRouter models with this key.", placeholder: "sk-or-…" },
	],
};

/** The connection Orchestrator Cloud uses for an agent: the personal "default" one. */
export function agentConnection(connections: readonly CloudProviderConnection[], agent: string): CloudProviderConnection | undefined {
	return connections.find((item) => item.provider === agent && item.label === "default");
}

/** Session create requires a valid personal credential for the harness. */
export function isAgentConnected(connections: readonly CloudProviderConnection[], agent: string): boolean {
	return agentConnection(connections, agent)?.validationState === "valid";
}

export type AgentConnectionState = "connected" | "invalid" | "pending" | "none";

export function agentConnectionState(connections: readonly CloudProviderConnection[], agent: string): AgentConnectionState {
	const connection = agentConnection(connections, agent);
	if (!connection) return "none";
	if (connection.validationState === "valid") return "connected";
	if (connection.validationState === "invalid") return "invalid";
	return "pending";
}

export function connectedAgents(connections: readonly CloudProviderConnection[]): CloudAgent[] {
	return CLOUD_AGENTS.filter((agent) => isAgentConnected(connections, agent));
}

/** How the credential was given, for the row's subtitle. */
export function credentialTypeLabel(connection: CloudProviderConnection | undefined): string | undefined {
	const type = connection?.config?.credentialType;
	if (typeof type !== "string") return undefined;
	switch (type) {
		case "oauth_token": return "Claude subscription";
		case "auth_json": return "ChatGPT sign-in";
		case "access_token": return "Access token";
		case "api_key": return "API key";
		case "opencode_api_key": return "OpenCode Zen key";
		case "anthropic_api_key": return "Anthropic key";
		case "openai_api_key": return "OpenAI key";
		case "openrouter_api_key": return "OpenRouter key";
		default: return type;
	}
}

/**
 * The harness a new session should default to: the project's configured one if
 * it is connected, otherwise the first connected agent in the desktop's
 * orchestrator order (codex → claude-code → cursor → opencode).
 */
export function defaultCloudAgent(connections: readonly CloudProviderConnection[], preferred?: string): CloudAgent | undefined {
	if (preferred && isCloudAgent(preferred) && isAgentConnected(connections, preferred)) return preferred;
	const order: CloudAgent[] = ["codex", "claude-code", "cursor", "opencode"];
	return order.find((agent) => isAgentConnected(connections, agent));
}

/** The title a new session gets: its prompt's first line, as the desktop names tasks (80 characters, the contract's limit). */
export function cloudTaskTitle(prompt: string, fallback: string): string {
	const first = prompt.trim().split("\n")[0]?.trim() ?? "";
	return (first || fallback).slice(0, 80);
}
