import { describe, expect, it, vi } from "vitest";

// config.ts reaches native storage at import time; connect.ts only needs its
// DEFAULT_CONFIG shape, so stub the natives out rather than pulling React
// Native into a pure-logic test.
vi.mock("@react-native-async-storage/async-storage", () => ({
	default: { getItem: vi.fn(), setItem: vi.fn(), removeItem: vi.fn() },
}));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(),
	setItemAsync: vi.fn(),
	deleteItemAsync: vi.fn(),
}));
import { configForEndpoint, connectHost } from "./connect";
import type { Endpoint } from "./endpoints";
import type { Host } from "./hosts";

const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: false };
const tunnel: Endpoint = { kind: "tunnel", host: "abc.trycloudflare.com", port: 443, secure: true };

const host = (over: Partial<Host> = {}): Host => ({
	id: "h_paired",
	name: "mbp",
	platform: "darwin",
	endpoints: [lan, tunnel],
	token: "pw",
	lastConnected: 1,
	...over,
});

describe("configForEndpoint", () => {
	// Everything downstream already speaks ServerConfig, so a won endpoint is
	// adapted into one rather than threading a new shape through api.ts and
	// mux.ts.
	it("adapts a plain endpoint into the shape the API layer expects", () => {
		expect(configForEndpoint(lan, "pw")).toMatchObject({
			host: "192.168.1.42",
			httpPort: "3011",
			secure: false,
			password: "pw",
		});
	});

	it("carries TLS through for a secure endpoint", () => {
		expect(configForEndpoint(tunnel, "pw")).toMatchObject({
			host: "abc.trycloudflare.com",
			httpPort: "443",
			secure: true,
		});
	});
});

describe("connectHost", () => {
	const deps = (over: Record<string, unknown> = {}) => ({
		findHost: vi.fn(async () => host()),
		race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h_paired" })),
		refreshEndpoints: vi.fn(async () => [lan, tunnel]),
		saveEndpoints: vi.fn(async (_hostId: string, _update: (stored: Endpoint[]) => Endpoint[]) => {}),
		adoptIdentity: vi.fn(async () => {}),
		touch: vi.fn(async () => {}),
		...over,
	});

	// What the deferred save writes, applied to the endpoints stored for the host.
	const saved = (d: ReturnType<typeof deps>, stored: Endpoint[] = host().endpoints) => {
		const call = d.saveEndpoints.mock.calls[0];
		expect(call?.[0]).toBe("h_paired");
		return call?.[1](stored);
	};

	it("returns a usable config for the endpoint that won", async () => {
		const d = deps();
		const got = await connectHost("h_paired", d);

		expect(got.ok).toBe(true);
		if (got.ok) expect(got.config.host).toBe("192.168.1.42");
	});

	// Refreshing on every successful connect is what makes a rotated tunnel
	// hostname or a changed LAN address heal without the user re-pairing.
	it("stores the endpoint list the daemon now advertises", async () => {
		const d = deps();
		await connectHost("h_paired", d);

		await vi.waitFor(() => expect(saved(d)).toEqual([lan, tunnel]));
	});

	it("keeps a working NAT endpoint when the daemon advertises an unreachable address of the same kind", async () => {
		const manual: Endpoint = { kind: "lan", host: "10.0.2.2", port: 13011, secure: false };
		const d = deps({
			findHost: vi.fn(async () => host({ endpoints: [manual] })),
			race: vi.fn(async () => ({ ok: true as const, endpoint: manual, hostId: "h_paired" })),
			refreshEndpoints: vi.fn(async () => [lan]),
		});

		await connectHost("h_paired", d);

		await vi.waitFor(() => expect(saved(d, [manual])).toEqual([lan, manual]));
	});

	// A quick tunnel takes ~34s to restart and settle, and the daemon advertises
	// no tunnel for that whole window. Replacing the stored list wholesale there
	// erased the only endpoint that works away from home — and nothing refreshes
	// again while the current one answers, so the phone kept the truncated list.
	it("keeps a known tunnel when the daemon is mid-restart and advertises none", async () => {
		const d = deps({ refreshEndpoints: vi.fn(async () => [lan]) });

		await connectHost("h_paired", d);

		await vi.waitFor(() => expect(saved(d)).toEqual([lan, tunnel]));
	});

	it("still adopts a rotated tunnel hostname over the stored one", async () => {
		const rotated: Endpoint = { kind: "tunnel", host: "xyz.trycloudflare.com", port: 443, secure: true };
		const d = deps({ refreshEndpoints: vi.fn(async () => [lan, rotated]) });

		await connectHost("h_paired", d);

		await vi.waitFor(() => expect(saved(d)).toEqual([lan, rotated]));
	});

	it("merges onto the endpoints stored when it saves, not the ones read at connect", async () => {
		const tailscale: Endpoint = { kind: "tailscale", host: "box.tail.ts.net", port: 3011, secure: false };
		const d = deps({ refreshEndpoints: vi.fn(async () => [lan]) });
		await connectHost("h_paired", d);
		// A re-pair stored a Tailscale address after this connect read the host.
		await vi.waitFor(() => expect(saved(d, [lan, tunnel, tailscale])).toEqual([lan, tunnel, tailscale]));
	});

	// A machine migrated from the single-server config has no id until it
	// connects once; this is where it learns one.
	it("records the identity a previously unverified machine reports", async () => {
		const d = deps({
			findHost: vi.fn(async () => host({ id: "" })),
			race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h_learned" })),
		});

		await connectHost("", d);

		expect(d.adoptIdentity).toHaveBeenCalledWith("", "h_learned");
	});

	it("does not rewrite the identity of an already-verified machine", async () => {
		const d = deps();
		await connectHost("h_paired", d);

		expect(d.adoptIdentity).not.toHaveBeenCalled();
	});

	it("reports failure when nothing answered", async () => {
		const d = deps({ race: vi.fn(async () => ({ ok: false as const, reason: "none-reachable" as const })) });
		const got = await connectHost("h_paired", d);

		expect(got.ok).toBe(false);
		if (!got.ok) expect(got.reason).toBe("none-reachable");
	});

	it("reports failure for a machine that is not paired", async () => {
		const d = deps({ findHost: vi.fn(async () => null) });
		const got = await connectHost("h_missing", d);

		expect(got.ok).toBe(false);
		if (!got.ok) expect(got.reason).toBe("unknown-host");
	});

	// A refresh that fails must not lose a working connection: the endpoints we
	// already have are still good enough to keep using.
	it("returns without waiting for the endpoint refresh", async () => {
		const d = deps({ refreshEndpoints: vi.fn(() => new Promise<never>(() => {})) });
		const got = await connectHost("h_paired", d);

		expect(got.ok).toBe(true);
		expect(d.touch).toHaveBeenCalledWith("h_paired");
		expect(d.saveEndpoints).not.toHaveBeenCalled();
	});

	it("stays connected when the endpoint refresh fails", async () => {
		const d = deps({
			refreshEndpoints: vi.fn(async () => {
				throw new Error("network blip");
			}),
		});
		const got = await connectHost("h_paired", d);

		expect(got.ok).toBe(true);
	});
});
