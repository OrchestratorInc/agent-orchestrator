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
		saveEndpoints: vi.fn(async () => {}),
		adoptIdentity: vi.fn(async () => {}),
		touch: vi.fn(async () => {}),
		...over,
	});

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

		expect(d.saveEndpoints).toHaveBeenCalledWith("h_paired", [lan, tunnel]);
	});

	it("keeps a working NAT endpoint when the daemon advertises an unreachable address of the same kind", async () => {
		const manual: Endpoint = { kind: "lan", host: "10.0.2.2", port: 13011, secure: false };
		const d = deps({
			findHost: vi.fn(async () => host({ endpoints: [manual] })),
			race: vi.fn(async () => ({ ok: true as const, endpoint: manual, hostId: "h_paired" })),
			refreshEndpoints: vi.fn(async () => [lan]),
		});

		await connectHost("h_paired", d);

		expect(d.saveEndpoints).toHaveBeenCalledWith("h_paired", [lan, manual]);
	});

	// A quick tunnel takes ~34s to restart and settle, and the daemon advertises
	// no tunnel for that whole window. Replacing the stored list wholesale there
	// erased the only endpoint that works away from home — and nothing refreshes
	// again while the current one answers, so the phone kept the truncated list.
	it("keeps a known tunnel when the daemon is mid-restart and advertises none", async () => {
		const d = deps({ refreshEndpoints: vi.fn(async () => [lan]) });

		await connectHost("h_paired", d);

		expect(d.saveEndpoints).toHaveBeenCalledWith("h_paired", [lan, tunnel]);
	});

	it("still adopts a rotated tunnel hostname over the stored one", async () => {
		const rotated: Endpoint = { kind: "tunnel", host: "xyz.trycloudflare.com", port: 443, secure: true };
		const d = deps({ refreshEndpoints: vi.fn(async () => [lan, rotated]) });

		await connectHost("h_paired", d);

		expect(d.saveEndpoints).toHaveBeenCalledWith("h_paired", [lan, rotated]);
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

describe("connectHost with device tokens", () => {
	const token = "aod_" + "a".repeat(64);
	const deps = (saved: Host, over: Record<string, unknown> = {}) => ({
		findHost: vi.fn(async () => saved),
		race: vi.fn(async () => ({ ok: true as const, endpoint: lan, hostId: "h_paired" })),
		refreshEndpoints: vi.fn(async (_config: { password: string }) => [lan, tunnel]),
		saveEndpoints: vi.fn(async () => {}),
		adoptIdentity: vi.fn(async () => {}),
		touch: vi.fn(async () => {}),
		pair: vi.fn(async (): Promise<import("./deviceToken").PairDeviceResult> => ({ kind: "paired", token, credential: "password" })),
		saveToken: vi.fn(async () => {}),
		...over,
	});

	it("swaps a saved pairing password for this phone's own token and keeps the password", async () => {
		const d = deps(host());
		const got = await connectHost("h_paired", d);

		expect(d.pair).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ password: "pw", hostId: "h_paired" }));
		expect(d.saveToken).toHaveBeenCalledExactlyOnceWith("h_paired", token, "pw");
		expect(got.ok && got.config.password).toBe(token);
		expect(d.refreshEndpoints).toHaveBeenCalledWith(expect.objectContaining({ password: token }));
	});

	it.each([["unsupported"], ["unauthorized"], ["offline"]] as const)("keeps the password when the swap answers %s", async (kind) => {
		const d = deps(host(), { pair: vi.fn(async () => ({ kind })) });
		const got = await connectHost("h_paired", d);
		expect(d.saveToken).not.toHaveBeenCalled();
		expect(got.ok && got.config.password).toBe("pw");
		// A refused password is not sent again for the refresh.
		expect(d.refreshEndpoints).toHaveBeenCalledTimes(kind === "unauthorized" ? 0 : 1);
	});

	it("leaves a device token and an account token alone", async () => {
		for (const saved of [host({ token }), host({ token: "a".repeat(64), accountUserId: "user-a" })]) {
			const d = deps(saved);
			const got = await connectHost("h_paired", d);
			expect(d.pair).not.toHaveBeenCalled();
			expect(d.saveToken).not.toHaveBeenCalled();
			expect(got.ok && got.config.password).toBe(saved.token);
		}
	});

	const refused = (code: string | undefined) => Object.assign(new Error("endpoint refresh returned 401"), { status: 401, code });
	const refreshRefusing = (code: string | undefined) => vi.fn(async (config: { password: string }) => {
		if (config.password === token) throw refused(code);
		return [lan, tunnel];
	});

	it("falls back to the kept password on a machine that no longer knows device tokens", async () => {
		const d = deps(host({ token, fallbackToken: "pw" }), {
			refreshEndpoints: refreshRefusing("BAD_PASSWORD"),
			pair: vi.fn(async () => ({ kind: "unsupported" as const })),
		});
		const got = await connectHost("h_paired", d);
		expect(d.saveToken).toHaveBeenCalledExactlyOnceWith("h_paired", "pw");
		expect(got.ok && got.config.password).toBe("pw");
		expect(d.refreshEndpoints.mock.calls.map(([config]) => config.password)).toEqual([token, "pw"]);
		expect(d.saveEndpoints).toHaveBeenCalledOnce();
	});

	it("keeps the device token when the machine refuses the kept password too", async () => {
		const d = deps(host({ token, fallbackToken: "pw" }), { refreshEndpoints: vi.fn(async () => { throw refused("BAD_PASSWORD"); }) });
		const got = await connectHost("h_paired", d);
		expect(d.saveToken).not.toHaveBeenCalled();
		expect(got.ok && got.config.password).toBe(token);
	});

	it.each([["removed this phone", "DEVICE_REVOKED"], ["sent no error code", undefined]])("does not use the kept password when the machine %s", async (_name, code) => {
		const d = deps(host({ token, fallbackToken: "pw" }), { refreshEndpoints: refreshRefusing(code) });
		const got = await connectHost("h_paired", d);
		expect(d.saveToken).not.toHaveBeenCalled();
		expect(d.pair).not.toHaveBeenCalled();
		expect(d.refreshEndpoints).toHaveBeenCalledOnce();
		expect(got.ok && got.config.password).toBe(token);
	});

	it("keeps the token when the refresh fails for another reason", async () => {
		for (const failure of [Object.assign(new Error("endpoint refresh returned 503"), { status: 503 }), new TypeError("Network request failed")]) {
			const d = deps(host({ token, fallbackToken: "pw" }), { refreshEndpoints: vi.fn(async () => { throw failure; }) });
			const got = await connectHost("h_paired", d);
			expect(d.saveToken).not.toHaveBeenCalled();
			expect(got.ok && got.config.password).toBe(token);
		}
	});

	it("makes no extra request for a working token with a kept password", async () => {
		const d = deps(host({ token, fallbackToken: "pw" }));
		const got = await connectHost("h_paired", d);
		expect(d.refreshEndpoints).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ password: token }));
		expect(d.pair).not.toHaveBeenCalled();
		expect(d.saveToken).not.toHaveBeenCalled();
		expect(got.ok && got.config.password).toBe(token);
	});
});
