import * as Crypto from "expo-crypto";
import * as SecureStore from "expo-secure-store";
import * as WebBrowser from "expo-web-browser";

// The desktop uses this public AuthKit client and HTTPS callback. The callback
// returns to ao-app://callback; mobile registers that scheme as well.
const CLIENT_ID = "client_01KZ3VRKC374HS91XGRDPT3671";
const REDIRECT_URI = "https://api.aoagents.dev/app/auth/return";
const APP_CALLBACK = "ao-app://callback";
const AUTH_URL = "https://api.workos.com/user_management/authorize";
const TOKEN_URL = "https://api.workos.com/user_management/authenticate";
const STORE_KEY = "ao.account.session";

export type Account = { id: string; email: string };
type StoredAccount = Account & { refreshToken: string };
type TokenResponse = {
	user?: { id?: string; email?: string };
	access_token?: string;
	refresh_token?: string;
	error?: string;
};

let accessToken: string | null = null;
let accessTokenExpiresAt = 0;
let refreshing: Promise<string | null> | null = null;

function tokenExpiry(token: string): number {
	try {
		const part = token.split(".")[1];
		if (part && typeof atob === "function") {
			const payload = JSON.parse(atob(part.replace(/-/g, "+").replace(/_/g, "/"))) as { exp?: number };
			if (typeof payload.exp === "number") return payload.exp * 1000;
		}
	} catch { /* An opaque development token uses the short cache window. */ }
	return Date.now() + 5 * 60_000;
}

function randomHex(): string {
	return Array.from(Crypto.getRandomBytes(32), (byte) => byte.toString(16).padStart(2, "0")).join("");
}

async function authenticate(body: Record<string, string>): Promise<TokenResponse> {
	const response = await fetch(TOKEN_URL, {
		method: "POST",
		headers: { "Content-Type": "application/json" },
		body: JSON.stringify({ client_id: CLIENT_ID, ...body }),
	});
	const result = await response.json() as TokenResponse;
	if (!response.ok) throw new Error(result.error === "invalid_grant" ? "Session expired. Sign in again." : "AO sign-in failed. Try again.");
	return result;
}

async function saveAccount(result: TokenResponse): Promise<Account> {
	const { id, email } = result.user ?? {};
	if (!id || !email || !result.refresh_token || !result.access_token) throw new Error("AO sign-in returned an incomplete session.");
	await SecureStore.setItemAsync(STORE_KEY, JSON.stringify({ id, email, refreshToken: result.refresh_token } satisfies StoredAccount));
	accessToken = result.access_token;
	accessTokenExpiresAt = tokenExpiry(result.access_token);
	return { id, email };
}

export async function loadAccount(): Promise<Account | null> {
	const raw = await SecureStore.getItemAsync(STORE_KEY);
	if (!raw) return null;
	try {
		const { id, email, refreshToken } = JSON.parse(raw) as StoredAccount;
		return id && email && refreshToken ? { id, email } : null;
	} catch {
		return null;
	}
}

export async function signInToAccount(): Promise<Account | null> {
	const verifier = randomHex();
	const state = randomHex();
	const digest = await Crypto.digestStringAsync(Crypto.CryptoDigestAlgorithm.SHA256, verifier, { encoding: Crypto.CryptoEncoding.BASE64 });
	const challenge = digest.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
	const url = new URL(AUTH_URL);
	for (const [key, value] of Object.entries({
		response_type: "code", client_id: CLIENT_ID, redirect_uri: REDIRECT_URI,
		provider: "authkit", prompt: "login", max_age: "0", state,
		code_challenge_method: "S256", code_challenge: challenge,
	})) url.searchParams.set(key, value);
	const result = await WebBrowser.openAuthSessionAsync(url.toString(), APP_CALLBACK);
	if (result.type !== "success") return null;
	const callback = new URL(result.url);
	if (callback.protocol !== "ao-app:" || callback.hostname !== "callback" || callback.searchParams.get("state") !== state) {
		throw new Error("AO sign-in response could not be verified.");
	}
	if (callback.searchParams.has("error")) throw new Error("AO sign-in was not completed.");
	const code = callback.searchParams.get("code");
	if (!code) throw new Error("AO sign-in did not return a code.");
	return saveAccount(await authenticate({ grant_type: "authorization_code", code, code_verifier: verifier }));
}

export async function signOutOfAccount(): Promise<void> {
	await SecureStore.deleteItemAsync(STORE_KEY);
	accessToken = null;
	accessTokenExpiresAt = 0;
}

/** Account APIs use this token; it must never be sent to an AO daemon. */
export async function getAccountAccessToken(): Promise<string | null> {
	if (accessToken && Date.now() < accessTokenExpiresAt - 60_000) return accessToken;
	if (refreshing) return refreshing;
	refreshing = refreshAccountToken();
	try { return await refreshing; } finally { refreshing = null; }
}

async function refreshAccountToken(): Promise<string | null> {
	const raw = await SecureStore.getItemAsync(STORE_KEY);
	if (!raw) return null;
	let stored: StoredAccount;
	try { stored = JSON.parse(raw) as StoredAccount; } catch { return null; }
	if (!stored.refreshToken) return null;
	try {
		const result = await authenticate({ grant_type: "refresh_token", refresh_token: stored.refreshToken });
		// A sign-out or a new sign-in during the request must not resurrect the
		// previous account's rotating credentials.
		const current = await SecureStore.getItemAsync(STORE_KEY);
		if (!current || (JSON.parse(current) as StoredAccount).refreshToken !== stored.refreshToken) return null;
		await saveAccount(result);
		return accessToken;
	} catch (error) {
		if (error instanceof Error && error.message.startsWith("Session expired")) await signOutOfAccount();
		throw error;
	}
}
