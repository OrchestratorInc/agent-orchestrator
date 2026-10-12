import { beforeEach, expect, it, vi } from "vitest";
const secure = new Map<string, string>();
const browser = vi.hoisted(() => ({ openAuthSessionAsync: vi.fn() }));
vi.mock("expo-crypto", () => ({
	getRandomBytes: () => new Uint8Array(32).fill(7),
	getRandomBytesAsync: async () => new Uint8Array(32).fill(7),
	digestStringAsync: async () => "YWJjZA==",
	CryptoDigestAlgorithm: { SHA256: "SHA-256" }, CryptoEncoding: { BASE64: "base64" },
}));
vi.mock("expo-web-browser", () => browser);
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(async (key: string) => secure.get(key) ?? null),
	setItemAsync: vi.fn(async (key: string, value: string) => void secure.set(key, value)),
	deleteItemAsync: vi.fn(async (key: string) => void secure.delete(key)),
}));
vi.mock("./accountHosts", () => ({ cancelAccountHostSync: vi.fn() }));
import { getAccountAccessToken, loadAccount, signInToAccount, signOutOfAccount } from "./account";
import { CLOUD_DEPLOYMENT } from "./cloud/config";
import { getAccountSessionOwner } from "./cloud/sessionOwner";
import { writeTokens } from "./cloud/tokens";

const token = `header.${btoa(JSON.stringify({ exp: 4_000_000_000 }))}.sig`;
const account = { id: "user_1", email: "person@example.com" };
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });
beforeEach(async () => {
	await signOutOfAccount();
	secure.clear(); vi.clearAllMocks(); vi.unstubAllGlobals();
	browser.openAuthSessionAsync.mockReset().mockImplementation(async (authorizationUrl: string, redirectUrl: string) =>
		({ type: "success", url: `${redirectUrl}?code=once&state=${new URL(authorizationUrl).searchParams.get("state")}` }));
	vi.stubGlobal("fetch", vi.fn<typeof fetch>(async (url) => String(url).endsWith("/me") ?
		json({ user: account, organizations: [] }) : json({ access_token: token, refresh_token: "refresh" })));
});
it("shares Cloud's AuthKit callback, verified account, and SecureStore credential owner", async () => {
	await expect(signInToAccount()).resolves.toEqual(account);
	const [authorizationUrl, redirect] = browser.openAuthSessionAsync.mock.calls[0] as [string, string];
	expect(redirect).toBe("aoagents.ao://callback");
	expect(new URL(authorizationUrl).searchParams.get("redirect_uri")).toBe(CLOUD_DEPLOYMENT.redirectUri);
	expect(getAccountSessionOwner(CLOUD_DEPLOYMENT).getSnapshot().account).toEqual(await loadAccount());
	expect(await getAccountAccessToken()).toBe(token);
	expect([...secure.keys()].every((key) => key.startsWith("ao.cloud.tokens.v2."))).toBe(true);
});
it("rejects wrong state before exchanging a code", async () => {
	browser.openAuthSessionAsync.mockResolvedValueOnce({ type: "success", url: "aoagents.ao://callback?code=once&state=wrong" });
	await expect(signInToAccount()).rejects.toThrow(/state/i);
	expect(fetch).not.toHaveBeenCalled();
});
it("shares rotating refresh with Cloud callers and uses only account APIs", async () => {
	await signInToAccount();
	await writeTokens({ accessToken: "expired", refreshToken: "old", expiresAt: 0 });
	const request = vi.fn<typeof fetch>(async () => json({ access_token: token, refresh_token: "rotated" }));
	vi.stubGlobal("fetch", request);
	expect(await Promise.all([getAccountAccessToken(), getAccountSessionOwner(CLOUD_DEPLOYMENT).getToken()])).toEqual([token, token]);
	expect(request).toHaveBeenCalledOnce();
	expect(request.mock.calls[0]?.[0]).toBe("https://api.workos.com/user_management/authenticate");
});
it("preserves ambiguous old account and daemon pairing records through fresh sign-in and sign-out", async () => {
	secure.set("ao.account.session", "old-account-record");
	secure.set("ao.connection.password", "paired");
	await signInToAccount();
	await signOutOfAccount();
	expect(await loadAccount()).toBeNull();
	expect(await getAccountAccessToken()).toBeNull();
	expect(secure.get("ao.account.session")).toBe("old-account-record");
	expect(secure.get("ao.connection.password")).toBe("paired");
});
