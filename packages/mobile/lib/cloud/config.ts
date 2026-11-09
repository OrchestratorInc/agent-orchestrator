/**
 * Where the phone reaches Orchestrator Cloud's control plane.
 *
 * One deployment per build: the WorkOS client the phone signs in with
 * (lib/account.ts) is the production one, and the control plane only accepts
 * tokens from the client it is configured for, so a build pointed elsewhere
 * must set both together. `EXPO_PUBLIC_AO_CLOUD_API_BASE_URL` is the same
 * variable account-host sync already reads.
 */
export const CLOUD_API_BASE_URL = (process.env.EXPO_PUBLIC_AO_CLOUD_API_BASE_URL || "https://api.aoagents.dev").replace(/\/+$/, "");

/** Every user route lives under this prefix; nothing else is ever called with the account token. */
export const CLOUD_API_PREFIX = "/api/cloud/v1";

/** The WebSocket origin for terminal streams: the same host, ws(s) instead of http(s). */
export function cloudWebSocketBase(baseUrl: string = CLOUD_API_BASE_URL): string {
	if (baseUrl.startsWith("https://")) return `wss://${baseUrl.slice("https://".length)}`;
	if (baseUrl.startsWith("http://")) return `ws://${baseUrl.slice("http://".length)}`;
	throw new Error("The Orchestrator Cloud address must be http(s).");
}
