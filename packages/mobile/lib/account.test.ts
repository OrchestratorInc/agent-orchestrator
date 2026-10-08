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

import { getAccountAccessToken, loadAccount, signInToAccount, signOutOfAccount } from "./account";

beforeEach(async () => {
	await signOutOfAccount();
	secure.clear();
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
	expect(authorization.searchParams.get("redirect_uri")).toBe("https://api.aoagents.dev/app/auth/return");
	expect(authorization.searchParams.get("code_challenge_method")).toBe("S256");
	expect(authorization.searchParams.get("code_challenge")).toBe("YWJjZA");
	expect(JSON.parse((fetch.mock.calls[0]?.[1] as RequestInit).body as string)).toMatchObject({
		grant_type: "authorization_code", code: "once", code_verifier: expect.any(String),
	});
	expect(await loadAccount()).toEqual({ id: "user_1", email: "person@example.com" });
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
	secure.set("ao.account.session", JSON.stringify({ id: "user_1", email: "person@example.com", refreshToken: "old" }));
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
