import type { RedactedProviderConnection } from "@aoagents/cloud-client";

/**
 * `ProviderName` (packages/cloud-client/src/schema.ts) is
 * `"daytona" | "claude-code" | "codex" | "cursor"`. `daytona` is a sandbox
 * provider connection — it provisions the VM a session runs in, it does not
 * run code — so it is deliberately excluded here. This is an explicit
 * allow-list rather than a comment on the provider list: `readyHarnesses`
 * and `hasConnectionForHarness` filter against it directly, so the exclusion
 * holds even if a future non-agent provider is added to `ProviderName` and
 * nobody updates this file.
 */
const CODING_AGENT_PROVIDERS = ["claude-code", "codex", "cursor"] as const;

function isCodingAgentProvider(provider: string): boolean {
	return (CODING_AGENT_PROVIDERS as readonly string[]).includes(provider);
}

/**
 * Whether this specific harness can run in the cloud.
 *
 * Deliberately per-harness. The desktop shipped an "any valid key" gate beside
 * a per-harness server check, so a Claude key plus a Codex selection passed the
 * UI and 422'd on create. Mobile does not repeat that.
 *
 * `RedactedProviderConnection` keys the connection by `provider` (not
 * `agent` as an earlier draft of this task assumed) — confirmed against
 * packages/cloud-client/src/schema.ts. `provider` shares its value space
 * with `harness` for the coding-agent providers, but also includes
 * "daytona", a sandbox provider that is never a harness. `harness` is typed
 * as a bare `string` everywhere else in this codebase (e.g.
 * spawn-composer-controls.types.ts, chat/types.ts), so it is kept that way
 * here too rather than narrowed to the coding-agent union — narrowing would
 * ripple into those call sites for no enforcement gain. Instead, any harness
 * outside `CODING_AGENT_PROVIDERS` returns false unconditionally, so the
 * guarantee holds regardless of what a caller passes in.
 */
export function hasConnectionForHarness(
	connections: RedactedProviderConnection[],
	harness: string,
): boolean {
	if (!isCodingAgentProvider(harness)) return false;
	return connections.some(
		(connection) => connection.provider === harness && connection.validationState === "valid",
	);
}

/** Every coding-agent harness with its own valid provider connection, in list order. */
export function readyHarnesses(connections: RedactedProviderConnection[]): string[] {
	return connections
		.filter((connection) => connection.validationState === "valid" && isCodingAgentProvider(connection.provider))
		.map((connection) => connection.provider);
}
