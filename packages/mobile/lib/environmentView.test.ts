import { describe, expect, it } from "vitest";
import type { ServerConfig } from "./config";
import type { Host } from "./hosts";

const host: Host = {
	id: "host-1",
	name: "Studio Mac",
	platform: "darwin",
	token: "secret",
	lastConnected: Date.parse("2026-10-08T10:00:00Z"),
	endpoints: [
		{ kind: "lan", host: "192.168.1.24", port: 3011, secure: false },
		{ kind: "tailscale", host: "studio.tail123.ts.net", port: 3011, secure: true },
		{ kind: "tunnel", host: "studio.example.com", port: 443, secure: true },
	],
};

const activeConfig: ServerConfig = {
	host: "studio.tail123.ts.net",
	httpPort: "3011",
	muxPort: "3012",
	secure: true,
	password: "secret",
	hostId: "host-1",
	endpointKind: "tailscale",
};

describe("environment overview presentation", () => {
	it("shows every saved route and identifies the active connection", async () => {
		const view = await import("./environmentView").catch(() => null);
		expect(view).not.toBeNull();
		if (!view) return;

		expect(view.environmentEndpointRows(host, activeConfig)).toEqual([
			{ key: "lan://192.168.1.24:3011", label: "LAN", address: "http://192.168.1.24:3011", active: false },
			{ key: "tailscale://studio.tail123.ts.net:3011", label: "Tailscale", address: "https://studio.tail123.ts.net:3011", active: true },
			{ key: "tunnel://studio.example.com:443", label: "Tunnel", address: "https://studio.example.com:443", active: false },
		]);
	});

	it("summarizes the fleet around the default machine", async () => {
		const view = await import("./environmentView").catch(() => null);
		expect(view).not.toBeNull();
		if (!view) return;

		expect(view.environmentSummary(0)).toBe("No machines");
		expect(view.environmentSummary(1, "Studio Mac")).toBe("Studio Mac · 1 machine");
		expect(view.environmentSummary(3, "Studio Mac")).toBe("Studio Mac · 3 machines");
	});

	it("presents daemon platform values as human-readable operating systems", async () => {
		const view = await import("./environmentView");
		expect(view.environmentPlatformLabel).toBeTypeOf("function");
		if (!view.environmentPlatformLabel) return;

		expect(view.environmentPlatformLabel("darwin")).toBe("macOS");
		expect(view.environmentPlatformLabel("win32")).toBe("Windows");
		expect(view.environmentPlatformLabel("linux")).toBe("Linux");
		expect(view.environmentPlatformLabel("freebsd")).toBe("freebsd");
		expect(view.environmentPlatformLabel("")).toBe("Unknown platform");
	});
});
