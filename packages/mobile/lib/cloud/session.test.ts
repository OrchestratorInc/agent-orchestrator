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
import { refreshWorkOSTokens } from "./signIn";
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

	it("does not return a fresh credential read before sign-out", async () => {
		let releaseRead!: (tokens: CloudTokens) => void;
		let stored: CloudTokens | null = fresh;
		const provider = createTokenProvider({
			read: () => new Promise((resolve) => { releaseRead = resolve; }),
			write: async (tokens) => { stored = tokens; },
			clear: async () => { stored = null; },
			refresh: async () => fresh,
			now: () => 0,
		});
		const pending = provider.getToken();
		await provider.signOut();
		releaseRead(fresh);
		expect(await pending).toBeNull();
		expect(stored).toBeNull();
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

	it.each([
		[400, { error: "invalid_grant", message: "Refresh token expired" }],
		[401, { message: "Unauthorized" }],
	])("clears a definitively rejected refresh (%s)", async (status, body) => {
		const { provider, stored } = harness(stale, () => refreshWorkOSTokens(
			{ clientId: "client", refreshToken: "r" },
			{ fetchImpl: async () => new Response(JSON.stringify(body), { status }) },
		));
		expect(await provider.getToken()).toBeNull();
		expect(stored()).toBeNull();
	});

	it.each([500, 503, 429, 400])("preserves credentials and allows retry after a non-grant refresh failure (%s)", async (status) => {
		const { provider, stored } = harness(stale, () => refreshWorkOSTokens(
			{ clientId: "client", refreshToken: "r" },
			{ fetchImpl: async () => new Response(JSON.stringify({ message: "Try again" }), { status }) },
		));
		await expect(provider.getToken()).rejects.toThrow("Try again");
		expect(stored()).toEqual(stale);
	});

	it("preserves the refresh token while offline, then recovers without signing in", async () => {
		let offline = true;
		const { provider, stored } = harness(stale, async () => {
			if (offline) throw new TypeError("Network request failed");
			return fresh;
		});
		await expect(provider.getToken()).rejects.toThrow("Network request failed");
		expect(stored()).toEqual(stale);
		offline = false;
		expect(await provider.getToken()).toBe("fresh");
		expect(stored()).toEqual(fresh);
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

	// A refresh whose fetch already returned, but whose write to storage is still
	// in flight when sign-out runs, must not leave the signed-out keystore holding
	// the new tokens: the write would be the last writer and resurrect the session.
	it("discards a refresh whose write completes after sign-out", async () => {
		let stored: CloudTokens | null = stale;
		let releaseWrite: () => void = () => {};
		const provider = createTokenProvider({
			read: async () => stored,
			write: (t) => new Promise<void>((resolve) => {
				releaseWrite = () => { stored = t; resolve(); };
			}),
			clear: async () => { stored = null; },
			refresh: async () => fresh,
			now: () => 0,
		});

		const pending = provider.getToken();
		// Let refresh() resolve and the write() call begin before signing out.
		await Promise.resolve();
		await Promise.resolve();
		const signedOut = provider.signOut();
		releaseWrite();
		await signedOut;

		expect(await pending).toBeNull();
		expect(stored).toBeNull();
	});
});
