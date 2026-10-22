import type { ServerConfig } from "./config";
import { endpointBaseUrl, endpointKey, type EndpointKind } from "./endpoints";
import type { Host } from "./hosts";

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

export function environmentSummary(count: number, defaultName?: string | null): string {
	if (count === 0) return "No machines";
	const total = `${count} ${count === 1 ? "machine" : "machines"}`;
	return defaultName ? `${defaultName} · ${total}` : total;
}

export function environmentPlatformLabel(platform: string): string {
	const normalized = platform.toLowerCase();
	if (normalized === "darwin" || normalized === "macos") return "macOS";
	if (normalized === "win32" || normalized === "windows") return "Windows";
	if (normalized === "linux") return "Linux";
	return platform || "Unknown platform";
}
