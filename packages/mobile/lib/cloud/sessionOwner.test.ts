import { describe, expect, it, vi } from "vitest";
vi.mock("expo-secure-store", () => ({ getItemAsync: async () => null, setItemAsync: async () => {}, deleteItemAsync: async () => {} }));
vi.mock("expo-web-browser", () => ({}));
vi.mock("expo-crypto", () => ({}));
import { createAccountSessionOwner } from "./sessionOwner";
import { createTokenProvider } from "./session";
import { createMobileCloudClient } from "./client";
import type { CloudTokens } from "./tokens";
import { CloudApiError } from "@aoagents/cloud-client";

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}
const issued: CloudTokens = { accessToken: "fresh", refreshToken: "refresh", expiresAt: 4_000_000_000_000 };
const account = { id: "u1", email: "one@example.test" };
function fixture() {
	const tokens = { getToken: vi.fn(async () => "token"), replace: vi.fn(async () => {}), signOut: vi.fn(async () => {}) };
	return { tokens, owner: createAccountSessionOwner({ tokens, readAccount: async () => account }) };
}

describe("account session owner", () => {
	it("retires retry records for the last verified account even if sign-out occurs during another login", async () => {
		const { tokens } = fixture();
		const retired = vi.fn(async () => {});
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => account, onSignOut: retired });
		await owner.initialize();
		const browser = deferred<CloudTokens>();
		const login = owner.signIn(() => browser.promise);
		await owner.signOut();
		expect(retired).toHaveBeenCalledWith(account);
		browser.resolve(issued); await login;
	});
	it("never returns a bearer after sign-out even if credential removal failed", async () => {
		const { owner, tokens } = fixture();
		await owner.initialize();
		tokens.signOut.mockRejectedValueOnce(new Error("Keychain unavailable"));
		await expect(owner.signOut()).rejects.toThrow("Keychain unavailable");
		tokens.getToken.mockClear();
		expect(await owner.getToken()).toBeNull();
		expect(tokens.getToken).not.toHaveBeenCalled();
	});
	it("drops a revoked persisted session after identity returns 401", async () => {
		const { tokens } = fixture();
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => { throw new CloudApiError(401, { error: "Unauthorized", code: "AUTH_REQUIRED", message: "Expired", requestId: "r1" }); } });
		await owner.initialize();
		expect(owner.getSnapshot().signedIn).toBe(false);
		expect(tokens.signOut).toHaveBeenCalledOnce();
	});
	it("does not accept browser success after sign-out", async () => {
		const browser = deferred<CloudTokens>();
		const { owner, tokens } = fixture();
		const login = owner.signIn(() => browser.promise);
		await owner.signOut();
		browser.resolve(issued);
		expect(await login).toBe(false);
		expect(tokens.replace).not.toHaveBeenCalled();
		expect(owner.getSnapshot().signedIn).toBe(false);
	});
	it("gives a newer sign-in precedence and drops the previous identity immediately", async () => {
		const { owner, tokens } = fixture();
		await owner.initialize();
		const browser = deferred<CloudTokens>();
		const first = owner.signIn(() => browser.promise);
		expect(owner.getSnapshot().account).toBeNull();
		await owner.signIn(async () => issued);
		browser.resolve({ ...issued, accessToken: "old" });
		expect(await first).toBe(false);
		expect(tokens.replace).toHaveBeenCalledExactlyOnceWith(issued);
		expect(owner.getSnapshot().account).toEqual(account);
	});
	it("keeps usable credentials on offline identity lookup", async () => {
		const tokens = { getToken: vi.fn(async () => "token"), replace: vi.fn(async () => {}), signOut: vi.fn(async () => {}) };
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => { throw new Error("Offline"); } });
		await owner.initialize();
		expect(owner.getSnapshot()).toMatchObject({ signedIn: true, error: "Offline", account: null });
		expect(tokens.signOut).not.toHaveBeenCalled();
	});
	it("shares one rotating refresh between account and Cloud callers", async () => {
		let stored: CloudTokens | null = { ...issued, expiresAt: 0 };
		const refresh = deferred<CloudTokens>();
		const refreshFn = vi.fn(() => refresh.promise);
		const tokens = createTokenProvider({ read: async () => stored, write: async (value) => { stored = value; }, clear: async () => { stored = null; }, refresh: refreshFn, now: Date.now });
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => account });
		const a = owner.getToken();
		const b = owner.getToken();
		await vi.waitFor(() => expect(refreshFn).toHaveBeenCalledOnce());
		refresh.resolve(issued);
		expect(await Promise.all([a, b])).toEqual(["fresh", "fresh"]);
	});
	it("does not send a replacement account bearer for an old bound request", async () => {
		const token = deferred<string | null>();
		let current = true;
		const fetch = vi.fn<typeof globalThis.fetch>();
		vi.stubGlobal("fetch", fetch);
		const client = createMobileCloudClient({ baseUrl: "https://cloud.test", tokens: { getToken: () => token.promise }, isCurrent: () => current });
		const request = client.getCurrentAccount().catch((error: Error) => error);
		current = false;
		token.resolve("new-account-token");
		expect(await request).toBeInstanceOf(Error);
		expect(fetch).not.toHaveBeenCalled();
		vi.unstubAllGlobals();
	});
});
