import { beforeEach, expect, it, vi } from "vitest";

const secure = new Map<string, string>();
const browser = vi.hoisted(() => ({ openAuthSessionAsync: vi.fn() }));

vi.mock("expo-crypto", () => ({
	getRandomBytes: () => new Uint8Array(32).fill(7),
	digestStringAsync: async () => "YWJjZA==",
	CryptoDigestAlgorithm: { SHA256: "SHA-256" },
	CryptoEncoding: { BASE64: "base64" },
}));
vi.mock("expo-web-browser", () => browser);
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(async (key: string) => secure.get(key) ?? null),
	setItemAsync: vi.fn(async (key: string, value: string) => void secure.set(key, value)),
	deleteItemAsync: vi.fn(async (key: string) => void secure.delete(key)),
}));

import { clearPreviousAccount, getAccountAccessToken, loadAccount, loadPreviousAccount, signInToAccount, signOutOfAccount } from "./account";

beforeEach(async () => {
	await signOutOfAccount();
	secure.clear();
	vi.clearAllMocks();
	vi.unstubAllGlobals();
	browser.openAuthSessionAsync.mockReset().mockImplementation(async (authorizationUrl: string, redirectUrl: string) => {
		const state = new URL(authorizationUrl).searchParams.get("state");
		return { type: "success", url: `${redirectUrl}?code=once&state=${state}` };
	});
});

it("uses the existing AO AuthKit callback and keeps refresh credentials in SecureStore", async () => {
	const fetch = vi.fn(async (_url: string, _init: RequestInit) => ({ ok: true, json: async () => ({
		user: { id: "user_1", email: "person@example.com" }, access_token: "access", refresh_token: "refresh",
	}) }));
	vi.stubGlobal("fetch", fetch);
	await expect(signInToAccount()).resolves.toEqual({ id: "user_1", email: "person@example.com" });
	const [authorizationUrl, redirectUrl] = browser.openAuthSessionAsync.mock.calls[0] as [string, string];
	const authorization = new URL(authorizationUrl);
	expect(redirectUrl).toBe("ao-app://callback");
	expect(authorization.searchParams.get("client_id")).toBe("client_01KZ3VRKTDKBZ3JA8AMKTF6W2X");
	expect(authorization.searchParams.get("redirect_uri")).toBe("https://api.aoagents.dev/app/auth/return");
	expect(authorization.searchParams.get("code_challenge_method")).toBe("S256");
	expect(authorization.searchParams.get("code_challenge")).toBe("YWJjZA");
	expect(JSON.parse((fetch.mock.calls[0]?.[1] as RequestInit).body as string)).toMatchObject({
		client_id: "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X", grant_type: "authorization_code", code: "once", code_verifier: expect.any(String),
	});
	expect(await loadAccount()).toEqual({ id: "user_1", email: "person@example.com" });
	expect(JSON.parse(secure.get("ao.account.session") ?? "{}").clientId).toBe("client_01KZ3VRKTDKBZ3JA8AMKTF6W2X");
	expect([...secure.values()].join(" ")).not.toContain("access");
	await signOutOfAccount();
	expect(await loadAccount()).toBeNull();
});

it("rejects a callback with the wrong state before exchanging a code", async () => {
	browser.openAuthSessionAsync.mockResolvedValueOnce({ type: "success", url: "ao-app://callback?code=once&state=wrong" });
	const fetch = vi.fn();
	vi.stubGlobal("fetch", fetch);
	await expect(signInToAccount()).rejects.toThrow(/verified/);
	expect(fetch).not.toHaveBeenCalled();
});

it("refreshes with the rotating token and never sends it to the AO daemon", async () => {
	secure.set("ao.account.session", JSON.stringify({ id: "user_1", email: "person@example.com", refreshToken: "old", clientId: "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X" }));
	const fetch = vi.fn(async (_url: string, _init: RequestInit) => ({ ok: true, json: async () => ({
		user: { id: "user_1", email: "person@example.com" }, access_token: "new-access", refresh_token: "new-refresh",
	}) }));
	vi.stubGlobal("fetch", fetch);
	expect(await getAccountAccessToken()).toBe("new-access");
	expect(JSON.parse((fetch.mock.calls[0]?.[1] as RequestInit).body as string)).toMatchObject({
		grant_type: "refresh_token", refresh_token: "old",
	});
	expect(JSON.parse(secure.get("ao.account.session") ?? "{}").refreshToken).toBe("new-refresh");
});

it("does not sign out a newer account when an old refresh expires late", async () => {
	secure.set("ao.account.session", JSON.stringify({ id: "old", email: "old@example.com", refreshToken: "old-refresh", clientId: "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X" }));
	let finish!: (value: unknown) => void;
	const fetch = vi.fn().mockImplementationOnce(() => new Promise((resolve) => { finish = resolve; }));
	vi.stubGlobal("fetch", fetch);
	const pending = getAccountAccessToken().catch(() => null);
	await vi.waitFor(() => expect(fetch).toHaveBeenCalledOnce());
	await signOutOfAccount();
	fetch.mockResolvedValueOnce({ ok: true, json: async () => ({ user: { id: "new", email: "new@example.com" }, access_token: "new-access", refresh_token: "new-refresh" }) });
	await signInToAccount();
	finish({ ok: false, json: async () => ({ error: "invalid_grant" }) });
	await pending;
	expect(await loadAccount()).toEqual({ id: "new", email: "new@example.com" });
	expect(await getAccountAccessToken()).toBe("new-access");
});

it("does not restore an account when sign-out races a refresh credential write", async () => {
	secure.set("ao.account.session", JSON.stringify({ id: "old", email: "old@example.com", refreshToken: "old-refresh", clientId: "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X" }));
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ user: { id: "old", email: "old@example.com" }, access_token: "access", refresh_token: "rotated" }) })));
	const storage = await import("expo-secure-store");
	let release!: () => void;
	const write = vi.mocked(storage.setItemAsync).mockImplementationOnce(async (key, value) => {
		await new Promise<void>((resolve) => { release = resolve; });
		secure.set(key, value);
	});
	const pending = getAccountAccessToken();
	await vi.waitFor(() => expect(write).toHaveBeenCalled());
	const signOut = signOutOfAccount();
	release();
	await Promise.all([pending, signOut]);
	expect(await loadAccount()).toBeNull();
	expect(await getAccountAccessToken()).toBeNull();
});

it("retires a session from the previous WorkOS client without asking WorkOS to refresh it", async () => {
	secure.set("ao.account.session", JSON.stringify({ id: "user_legacy", email: "person@example.com", refreshToken: "legacy-refresh" }));
	const fetch = vi.fn();
	vi.stubGlobal("fetch", fetch);
	expect(await getAccountAccessToken()).toBeNull();
	expect(await loadAccount()).toBeNull();
	expect(fetch).not.toHaveBeenCalled();
	expect(secure.has("ao.account.session")).toBe(false);
	// Kept so account-host sync can move this phone's machines to the new user ID.
	expect(await loadPreviousAccount()).toEqual({ id: "user_legacy", email: "person@example.com" });
	await clearPreviousAccount("someone-else");
	expect(await loadPreviousAccount()).not.toBeNull();
	await clearPreviousAccount("user_legacy");
	expect(await loadPreviousAccount()).toBeNull();
});

it("remembers the previous account when a new sign-in replaces an old-client session", async () => {
	secure.set("ao.account.session", JSON.stringify({ id: "user_legacy", email: "person@example.com", refreshToken: "legacy-refresh", clientId: "client_other" }));
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({
		user: { id: "user_new", email: "person@example.com" }, access_token: "access", refresh_token: "refresh",
	}) })));
	await expect(signInToAccount()).resolves.toEqual({ id: "user_new", email: "person@example.com" });
	expect(await loadPreviousAccount()).toEqual({ id: "user_legacy", email: "person@example.com" });
	expect(await loadAccount()).toEqual({ id: "user_new", email: "person@example.com" });
});
