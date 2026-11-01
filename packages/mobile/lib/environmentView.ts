import type { ServerConfig } from "./config";
import { endpointBaseUrl, endpointKey, type EndpointKind } from "./endpoints";
import type { Host } from "./hosts";
import type { Theme } from "./theme";

export type EnvironmentEndpointRow = {
	key: string;
	label: string;
	address: string;
	active: boolean;
};

const endpointLabels: Record<EndpointKind, string> = {
	lan: "LAN",
	tailscale: "Tailscale",
	tunnel: "Tunnel",
	relay: "Relay",
};

export function environmentEndpointRows(host: Host, activeConfig?: ServerConfig | null): EnvironmentEndpointRow[] {
	return host.endpoints.map((endpoint) => ({
		key: endpointKey(endpoint),
		label: endpointLabels[endpoint.kind] ?? endpoint.kind,
		address: endpointBaseUrl(endpoint),
		active: activeConfig?.hostId === host.id &&
			activeConfig.host === endpoint.host &&
			Number(activeConfig.httpPort) === endpoint.port &&
			Boolean(activeConfig.secure) === endpoint.secure,
	}));
}

/** The route a machine is reached by: the one it is connected over, else the first saved. */
export function primaryEnvironmentEndpoint(rows: readonly EnvironmentEndpointRow[]): EnvironmentEndpointRow | undefined {
	return rows.find((row) => row.active) ?? rows[0];
}

/** An endpoint URL as people read it: the bare host, and the host with its port. */
export function endpointDisplayAddress(address: string): { host: string; authority: string } {
	try {
		const parsed = new URL(address);
		return { host: parsed.hostname, authority: parsed.host };
	} catch {
		const authority = address.replace(/^[a-z][a-z\d+.-]*:\/\//i, "").replace(/\/$/, "");
		return { host: authority.replace(/:\d+$/, ""), authority };
	}
}

export type EnvironmentStatus = { kind: "connected" | "connecting" | "offline"; label: string };

/** A machine's live connection, named in words so it never reads by colour alone. */
export function environmentStatus(snapshot?: { connection: "closed" | "connecting" | "open"; loading: boolean } | null): EnvironmentStatus {
	if (snapshot?.connection === "open") return { kind: "connected", label: "Connected" };
	if (snapshot?.connection === "connecting" || snapshot?.loading) return { kind: "connecting", label: "Connecting" };
	return { kind: "offline", label: "Offline" };
}

/** The colour a status is drawn in, beside the word that names it. */
export function environmentStatusColor(t: Pick<Theme, "green" | "red" | "textSecondary">, status: EnvironmentStatus): string {
	if (status.kind === "connected") return t.green;
	if (status.kind === "offline") return t.red;
	return t.textSecondary;
}

/** What forgetting a machine does, for its confirmation. */
export const FORGET_MACHINE_MESSAGE = "Remove this machine's saved connection from this phone? Its desktop workers and files won't be deleted. You can pair it again later.\n\nNotification cleanup will finish when the machine is reachable.";

/**
 * Why an account-synced machine's route can't be edited: account sync rewrites
 * the route and credential from AO Cloud, so an edit here would silently
 * revert. Its name and icon stay phone-local.
 */
export const ACCOUNT_MANAGED_NOTE = "This machine's address and password come from your Orchestrator Cloud account and update automatically. To connect another way, use Reconnect to scan a new code.";

export function environmentPlatformLabel(platform: string): string {
	const normalized = platform.toLowerCase();
	if (normalized === "darwin" || normalized === "macos") return "macOS";
	if (normalized === "win32" || normalized === "windows") return "Windows";
	if (normalized === "linux") return "Linux";
	return platform || "Unknown platform";
}
