import { describe, expect, it, vi } from "vitest";
import {
	buildWorkOSAuthUrl,
	exchangeWorkOSCode,
	registerWithLocalAuth,
	signInWithLocalAuth,
} from "./signIn";

// A minimal unsigned JWT with the given `exp` (epoch seconds) claim, matching
// the shape WorkOS access tokens carry. The signature segment is never
// verified client-side, so any placeholder works here.
function fakeJwt(exp: number): string {
	const header = btoa(JSON.stringify({ alg: "none" }));
	const payload = btoa(JSON.stringify({ exp }));
	return `${header}.${payload}.sig`;
}

// JWT with base64url-encoded payload to test url-safe character handling.
function fakeJwtWithBase64Url(exp: number): string {
	const header = "eyJhbGciOiJub25lIn0";  // {"alg":"none"}, no padding
	const payloadStr = JSON.stringify({ exp, test: "base64url" });
	const standard = btoa(payloadStr);
	// Convert to base64url: replace + with -, / with _, remove padding
	const urlSafe = standard.replace(/\+/g, "-").replace(/\//g, "_").replace(/=/g, "");
	return `${header}.${urlSafe}.sig`;
}

describe("signInWithLocalAuth", () => {
	it("posts credentials and maps the response to stored tokens", async () => {
		const fetchImpl = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(
			JSON.stringify({
				token: "tok",
				expiresAt: "1970-01-01T00:16:40.000Z", // 1_000_000ms
				user: { id: "u1", email: "dev@example.com", displayName: "Dev", authProvider: "local" },
				organizations: [],
			}),
			{ status: 200, headers: { "content-type": "application/json" } },
		));
		const tokens = await signInWithLocalAuth(
			"http://127.0.0.1:8081", "dev@example.com", "correct-horse-battery",
			{ fetchImpl, now: () => 0 },
		);
		expect(fetchImpl).toHaveBeenCalledWith(
			"http://127.0.0.1:8081/api/cloud/v1/auth/local/login",
			expect.objectContaining({ method: "POST" }),
		);
		const [, init] = fetchImpl.mock.calls[0]!;
		expect(JSON.parse(init!.body as string)).toEqual({
			email: "dev@example.com", password: "correct-horse-battery",
		});
		expect(tokens).toEqual({ accessToken: "tok", expiresAt: 1_000_000 });
	});

	it("throws a readable error when the control plane rejects the credentials", async () => {
		const fetchImpl = async () => new Response(
			JSON.stringify({ message: "The email or password is incorrect." }),
			{ status: 401, headers: { "content-type": "application/json" } },
		);
		await expect(
			signInWithLocalAuth("http://127.0.0.1:8081", "a@b.c", "nope", { fetchImpl, now: () => 0 }),
		).rejects.toThrow("The email or password is incorrect.");
	});

	it("throws when expiresAt is not a valid RFC3339 timestamp", async () => {
		const fetchImpl = async () => new Response(
			JSON.stringify({
				token: "tok",
				expiresAt: "not-a-timestamp",
				user: { id: "u1", email: "dev@example.com", displayName: "Dev", authProvider: "local" },
				organizations: [],
			}),
			{ status: 200, headers: { "content-type": "application/json" } },
		);
		await expect(
			signInWithLocalAuth("http://127.0.0.1:8081", "dev@example.com", "pass", { fetchImpl, now: () => 0 }),
		).rejects.toThrow("could not be understood");
	});
});

describe("registerWithLocalAuth", () => {
	it("posts the full registration payload the server requires", async () => {
		const fetchImpl = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(
			JSON.stringify({
				token: "tok2",
				expiresAt: "1970-01-01T00:16:40.000Z",
				user: { id: "u1", email: "dev@example.com", displayName: "Dev", authProvider: "local" },
				organizations: [{ id: "o1", slug: "acme", displayName: "Acme", role: "owner" }],
			}),
			{ status: 201, headers: { "content-type": "application/json" } },
		));
		const tokens = await registerWithLocalAuth(
			"http://127.0.0.1:8081",
			{
				email: "dev@example.com", password: "correct-horse-battery",
				displayName: "Dev", orgSlug: "acme", orgName: "Acme",
			},
			{ fetchImpl, now: () => 0 },
		);
		expect(fetchImpl).toHaveBeenCalledWith(
			"http://127.0.0.1:8081/api/cloud/v1/auth/local/register",
			expect.objectContaining({ method: "POST" }),
		);
		const [, init] = fetchImpl.mock.calls[0]!;
		expect(JSON.parse(init!.body as string)).toEqual({
			email: "dev@example.com", password: "correct-horse-battery",
			displayName: "Dev", orgSlug: "acme", orgName: "Acme",
		});
		expect(tokens).toEqual({ accessToken: "tok2", expiresAt: 1_000_000 });
	});
});

describe("buildWorkOSAuthUrl", () => {
	it("builds a PKCE authorization URL", () => {
		const url = new URL(buildWorkOSAuthUrl({
			clientId: "client_123",
			redirectUri: "aomobile://callback",
			codeChallenge: "chal",
			state: "st",
		}));
		expect(url.origin + url.pathname).toBe("https://api.workos.com/user_management/authorize");
		expect(url.searchParams.get("client_id")).toBe("client_123");
		expect(url.searchParams.get("redirect_uri")).toBe("aomobile://callback");
		expect(url.searchParams.get("code_challenge")).toBe("chal");
		expect(url.searchParams.get("code_challenge_method")).toBe("S256");
		expect(url.searchParams.get("response_type")).toBe("code");
		expect(url.searchParams.get("state")).toBe("st");
	});
});

describe("exchangeWorkOSCode", () => {
	it("posts the PKCE token exchange with no client secret", async () => {
		const fetchImpl = vi.fn(async (_url: RequestInfo | URL, _init?: RequestInit) => new Response(
			JSON.stringify({ access_token: fakeJwt(1_700_000_000), refresh_token: "refresh-1" }),
			{ status: 200, headers: { "content-type": "application/json" } },
		));
		const tokens = await exchangeWorkOSCode(
			{ clientId: "client_123", code: "auth-code", codeVerifier: "verifier" },
			{ fetchImpl },
		);
		expect(fetchImpl).toHaveBeenCalledWith(
			"https://api.workos.com/user_management/authenticate",
			expect.objectContaining({ method: "POST" }),
		);
		const [, init] = fetchImpl.mock.calls[0]!;
		expect(JSON.parse(init!.body as string)).toEqual({
			client_id: "client_123",
			grant_type: "authorization_code",
			code: "auth-code",
			code_verifier: "verifier",
		});
		expect(tokens).toEqual({
			accessToken: fakeJwt(1_700_000_000),
			refreshToken: "refresh-1",
			expiresAt: 1_700_000_000_000,
		});
	});

	it("throws when the access token cannot be decoded", async () => {
		const fetchImpl = async () => new Response(
			JSON.stringify({ access_token: "not-a-jwt" }),
			{ status: 200, headers: { "content-type": "application/json" } },
		);
		await expect(
			exchangeWorkOSCode(
				{ clientId: "client_123", code: "auth-code", codeVerifier: "verifier" },
				{ fetchImpl, now: () => 1_000_000 },
			),
		).rejects.toThrow("could not be understood");
	});

	it("throws a readable error when the exchange is rejected", async () => {
		const fetchImpl = async () => new Response(
			JSON.stringify({ message: "The authorization code is invalid or has expired." }),
			{ status: 400, headers: { "content-type": "application/json" } },
		);
		await expect(
			exchangeWorkOSCode({ clientId: "client_123", code: "bad", codeVerifier: "verifier" }, { fetchImpl }),
		).rejects.toThrow("The authorization code is invalid or has expired.");
	});

	it("decodes JWT with base64url-encoded payload and padding", async () => {
		// Test base64url conversion: - to +, _ to /, and padding restoration
		const jwt = fakeJwtWithBase64Url(1700000000);
		const fetchImpl = vi.fn(async () => new Response(
			JSON.stringify({ access_token: jwt, refresh_token: "refresh-1" }),
			{ status: 200, headers: { "content-type": "application/json" } },
		));
		const tokens = await exchangeWorkOSCode(
			{ clientId: "client_123", code: "auth-code", codeVerifier: "verifier" },
			{ fetchImpl },
		);
		expect(tokens.expiresAt).toBe(1700000000000);
	});

	it("throws when JWT exp claim is missing", async () => {
		const header = btoa(JSON.stringify({ alg: "none" }));
		const payload = btoa(JSON.stringify({ sub: "user123" }));  // no exp claim
		const invalidJwt = `${header}.${payload}.sig`;
		const fetchImpl = async () => new Response(
			JSON.stringify({ access_token: invalidJwt }),
			{ status: 200, headers: { "content-type": "application/json" } },
		);
		await expect(
			exchangeWorkOSCode(
				{ clientId: "client_123", code: "auth-code", codeVerifier: "verifier" },
				{ fetchImpl },
			),
		).rejects.toThrow("could not be understood");
	});

	it("throws when JWT exp claim is not a number", async () => {
		const header = btoa(JSON.stringify({ alg: "none" }));
		const payload = btoa(JSON.stringify({ exp: "1700000000" }));  // string instead of number
		const invalidJwt = `${header}.${payload}.sig`;
		const fetchImpl = async () => new Response(
			JSON.stringify({ access_token: invalidJwt }),
			{ status: 200, headers: { "content-type": "application/json" } },
		);
		await expect(
			exchangeWorkOSCode(
				{ clientId: "client_123", code: "auth-code", codeVerifier: "verifier" },
				{ fetchImpl },
			),
		).rejects.toThrow("could not be understood");
	});
});
