import { beforeEach, describe, expect, it, vi } from "vitest";
const operationStorage = vi.hoisted(() => new Map<string, string>());
const secureStorage = vi.hoisted(() => new Map<string, string>());
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => operationStorage.get(key) ?? null,
	setItem: async (key: string, value: string) => { operationStorage.set(key, value); },
	removeItem: async (key: string) => { operationStorage.delete(key); },
	getAllKeys: async () => [...operationStorage.keys()],
} }));
vi.mock("expo-secure-store", () => ({ getItemAsync: async (key: string) => secureStorage.get(key) ?? null, setItemAsync: async (key: string, value: string) => { secureStorage.set(key, value); }, deleteItemAsync: async (key: string) => { secureStorage.delete(key); } }));
vi.mock("expo-web-browser", () => ({}));
vi.mock("expo-crypto", () => ({}));
import { createAccountSessionOwner, getAccountSessionOwner } from "./sessionOwner";
import { resolveCloudDeployment } from "./deployment";
import { addReviewComment, cloudReviewKey, reviewComments } from "../files/reviewStore";
import { createTokenProvider } from "./session";
import { createMobileCloudClient } from "./client";
import type { CloudTokens } from "./tokens";
import { CloudApiError } from "@aoagents/cloud-client";
import { clearCloudOperationsForAccount, prepareCloudOperation, readCloudOperation, reserveCloudOperation } from "./operation";

beforeEach(() => { operationStorage.clear(); secureStorage.clear(); });

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
	it("removes account-scoped file reviews on explicit deployment sign-out", async () => {
		const deployment = resolveCloudDeployment({ cloudUrl: "https://reviews-cleanup.test", development: true });
		vi.stubGlobal("fetch", async () => new Response(JSON.stringify({ user: account })));
		try {
			const owner = getAccountSessionOwner(deployment);
			expect(await owner.signIn(async () => issued)).toBe(true);
			const key = cloudReviewKey('["https://reviews-cleanup.test","u1","org"]', "review");
			addReviewComment(key, { label: "a", feedback: "private", target: { path: "a.ts", side: "new", line: 1 } });
			await owner.signOut();
			expect(reviewComments(key)).toEqual([]);
		} finally { vi.unstubAllGlobals(); }
	});
	it.each([
		{ name: "the same account", nextAccount: account },
		{ name: "another account", nextAccount: { id: "u2", email: "two@example.test" } },
	])("preserves a new reservation and bearer when signing back in to $name", async ({ nextAccount }) => {
		const cleanup = deferred<void>();
		const tokenClear = deferred<void>();
		let stored: CloudTokens | null = issued;
		let identity = account;
		const tokens = createTokenProvider({ read: async () => stored, write: async (value) => { stored = value; },
			clear: async () => { await tokenClear.promise; stored = null; }, refresh: async () => issued, now: Date.now });
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => identity, onSignOut: async (retired) => {
			await cleanup.promise;
			if (retired) await clearCloudOperationsForAccount("https://cloud.test", retired.id);
		} });
		await owner.initialize();
		const oldScope = '["https://cloud.test","u1","org-1"]';
		await reserveCloudOperation("project-create/old", prepareCloudOperation(undefined, oldScope, { displayName: "Old" }, () => "old-key"));
		const signOut = owner.signOut();
		identity = nextAccount;
		const freshTokens = { ...issued, accessToken: "new-login" };
		const login = owner.signIn(async () => freshTokens);
		tokenClear.resolve();
		await Promise.resolve(); await Promise.resolve();
		expect(owner.getSnapshot()).toMatchObject({ account: null, signedIn: null, busy: true });
		cleanup.resolve();
		await signOut;
		expect(await login).toBe(true);
		const newScope = JSON.stringify(["https://cloud.test", nextAccount.id, "org-1"]);
		await reserveCloudOperation("project-create/new", prepareCloudOperation(undefined, newScope, { displayName: "New" }, () => "new-key"));
		expect(await readCloudOperation(oldScope, "project-create/old")).toBeNull();
		expect(await readCloudOperation(newScope, "project-create/new")).toMatchObject({ key: "new-key", payload: { displayName: "New" } });
		expect(stored).toEqual(freshTokens);
		expect(await owner.getToken()).toBe("new-login");
	});
	it("keeps the next login unverified until signed-out account cleanup finishes", async () => {
		const cleanup = deferred<void>();
		const { tokens } = fixture();
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => account, onSignOut: () => cleanup.promise });
		await owner.initialize();
		const signOut = owner.signOut();
		const login = owner.signIn(async () => issued);
		await Promise.resolve(); await Promise.resolve();
		expect(owner.getSnapshot()).toMatchObject({ account: null, signedIn: null, busy: true });
		expect(tokens.replace).not.toHaveBeenCalled();
		cleanup.resolve();
		await signOut;
		expect(await login).toBe(true);
		expect(owner.getSnapshot()).toMatchObject({ account, signedIn: true, busy: false });
	});
	it("waits for account cleanup even if token deletion fails before another login", async () => {
		const cleanup = deferred<void>();
		const { tokens } = fixture();
		tokens.signOut.mockRejectedValueOnce(new Error("Keychain unavailable"));
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => account, onSignOut: () => cleanup.promise });
		await owner.initialize();
		const signOut = owner.signOut().catch((error: Error) => error);
		const login = owner.signIn(async () => issued);
		await Promise.resolve(); await Promise.resolve();
		expect(owner.getSnapshot()).toMatchObject({ account: null, busy: true });
		expect(tokens.replace).not.toHaveBeenCalled();
		cleanup.resolve();
		expect(await signOut).toMatchObject({ message: "Keychain unavailable" });
		expect(await login).toBe(true);
	});
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
