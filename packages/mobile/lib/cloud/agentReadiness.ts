import type { RedactedProviderConnection } from "@aoagents/cloud-client";

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
 * with `harness` for the coding-agent providers ("claude-code" | "codex" |
 * "cursor"); "daytona" is a sandbox provider connection, never a harness, so
 * it can never match here.
 */
export function hasConnectionForHarness(
	connections: RedactedProviderConnection[],
	harness: string,
): boolean {
	return connections.some(
		(connection) => connection.provider === harness && connection.validationState === "valid",
	);
}

/** Every harness with its own valid provider connection, in list order. */
export function readyHarnesses(connections: RedactedProviderConnection[]): string[] {
	return connections
		.filter((connection) => connection.validationState === "valid")
		.map((connection) => connection.provider);
}
