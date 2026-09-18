import { describe, expect, it, vi } from "vitest";

// session.ts imports `isExpired` and the `CloudTokens` type from "./tokens", which in turn
// imports "expo-secure-store". That module pulls in react-native's Flow-syntax entrypoint,
// which vitest cannot parse, so it must be mocked here even though this suite never touches
// the device keystore directly (see lib/cloud/tokens.test.ts for the same pattern).
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(async () => null),
	setItemAsync: vi.fn(async () => {}),
	deleteItemAsync: vi.fn(async () => {}),
}));

import { createTokenProvider } from "./session";
import type { CloudTokens } from "./tokens";

const fresh: CloudTokens = { accessToken: "fresh", refreshToken: "r", expiresAt: 10_000_000 };
const stale: CloudTokens = { accessToken: "stale", refreshToken: "r", expiresAt: 0 };

function harness(initial: CloudTokens | null, refresh: () => Promise<CloudTokens>) {
	let stored = initial;
	const refreshSpy = vi.fn(refresh);
	const provider = createTokenProvider({
		read: async () => stored,
		write: async (t) => { stored = t; },
		clear: async () => { stored = null; },
		refresh: refreshSpy,
		now: () => 0,
	});
	return { provider, refreshSpy, stored: () => stored };
}

describe("createTokenProvider", () => {
	it("returns a live token without refreshing", async () => {
		const { provider, refreshSpy } = harness(fresh, async () => fresh);
		expect(await provider.getToken()).toBe("fresh");
		expect(refreshSpy).not.toHaveBeenCalled();
	});

	it("returns null when signed out", async () => {
		const { provider } = harness(null, async () => fresh);
		expect(await provider.getToken()).toBeNull();
	});

	it("refreshes an expired token and stores the result", async () => {
		const { provider, stored } = harness(stale, async () => fresh);
		expect(await provider.getToken()).toBe("fresh");
		expect(stored()).toEqual(fresh);
	});

	// Every screen asks for a token at once on resume. One refresh, not eight.
	it("shares one refresh between concurrent callers", async () => {
		const { provider, refreshSpy } = harness(stale, async () => fresh);
		const results = await Promise.all([provider.getToken(), provider.getToken(), provider.getToken()]);
		expect(results).toEqual(["fresh", "fresh", "fresh"]);
		expect(refreshSpy).toHaveBeenCalledTimes(1);
	});

	it("signs out and reports null when the refresh is rejected", async () => {
		const { provider, stored } = harness(stale, async () => { throw new Error("invalid_grant"); });
		expect(await provider.getToken()).toBeNull();
		expect(stored()).toBeNull();
	});

	// A refresh that lands after sign-out must not resurrect the session.
	it("discards a refresh that completes after sign-out", async () => {
		let release: (t: CloudTokens) => void = () => {};
		const { provider, stored } = harness(stale, () => new Promise((r) => { release = r; }));
		const pending = provider.getToken();
		await provider.signOut();
		release(fresh);
		expect(await pending).toBeNull();
		expect(stored()).toBeNull();
	});
});
