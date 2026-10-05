import { CLOUD_API_PREFIX } from "./config";
import type { CloudTokens } from "./tokens";

type Deps = { fetchImpl?: typeof fetch; now?: () => number };

/** Only a definitive rejection of the refresh grant requires a new login. */
export class RefreshRejectedError extends Error {}

async function errorMessage(response: Response, fallback: string): Promise<string> {
	try {
		const body = (await response.json()) as { message?: unknown };
		if (typeof body?.message === "string" && body.message !== "") return body.message;
	} catch {
		// Non-JSON body: keep the fallback.
	}
	return fallback;
}

/**
 * The local control plane's authResponse (cloud/internal/httpapi/auth_handlers.go)
 * returns an opaque `token` plus an absolute `expiresAt` timestamp — not the
 * `accessToken`/`expiresIn` shape an OAuth client normally expects.
 */
type LocalAuthResponse = { token: string; expiresAt: string };

function parseAbsoluteExpiry(value: string): number {
	const parsed = Date.parse(value);
	if (!Number.isFinite(parsed)) {
		throw new Error("The server's response could not be understood.");
	}
	return parsed;
}

async function localAuth(
	path: "login" | "register",
	baseUrl: string,
	body: Record<string, string>,
	{ fetchImpl = fetch, now = Date.now }: Deps,
): Promise<CloudTokens> {
	const response = await fetchImpl(`${baseUrl}${CLOUD_API_PREFIX}/auth/local/${path}`, {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify(body),
	});
	if (!response.ok) {
		throw new Error(await errorMessage(response, `Sign-in failed (${response.status}).`));
	}
	const data = (await response.json()) as LocalAuthResponse;
	return { accessToken: data.token, expiresAt: parseAbsoluteExpiry(data.expiresAt) };
}

/** Dev-only email/password sign-in against a loopback control plane. */
export function signInWithLocalAuth(
	baseUrl: string, email: string, password: string, deps: Deps = {},
): Promise<CloudTokens> {
	return localAuth("login", baseUrl, { email, password }, deps);
}

export type RegisterLocalAuthInput = {
	email: string;
	password: string;
	/** Required by the server (1-120 chars); there is no default. */
	displayName: string;
	/** Required by the server: lowercase, `^[a-z0-9][a-z0-9-]{1,62}$`. */
	orgSlug: string;
	/** Required by the server (1-120 chars). */
	orgName: string;
};

export function registerWithLocalAuth(
	baseUrl: string, input: RegisterLocalAuthInput, deps: Deps = {},
): Promise<CloudTokens> {
	return localAuth("register", baseUrl, input, deps);
}

export function buildWorkOSAuthUrl(input: {
	clientId: string; redirectUri: string; codeChallenge: string; state: string;
}): string {
	const url = new URL("https://api.workos.com/user_management/authorize");
	url.searchParams.set("client_id", input.clientId);
	url.searchParams.set("redirect_uri", input.redirectUri);
	url.searchParams.set("response_type", "code");
	url.searchParams.set("provider", "authkit");
	url.searchParams.set("prompt", "login");
	url.searchParams.set("code_challenge", input.codeChallenge);
	url.searchParams.set("code_challenge_method", "S256");
	url.searchParams.set("state", input.state);
	return url.toString();
}

/**
 * Decodes a JWT's `exp` claim (epoch seconds) without pulling in a JWT or
 * base64 dependency. Mirrors frontend/src/main/cloud-auth.ts's jwtPayload —
 * WorkOS's /user_management/authenticate response carries no `expires_in`
 * field (confirmed by reading @workos-inc/node's
 * deserializeAuthenticationResponse); the desktop app also derives expiry by
 * decoding the access token instead of trusting a response field that does
 * not exist.
 */
function decodeJwtExpiryMs(token: string): number {
	try {
		const payload = token.split(".")[1];
		if (!payload) throw new Error("Missing JWT payload segment.");
		const base64 = payload.replace(/-/g, "+").replace(/_/g, "/");
		const padded = base64 + "=".repeat((4 - (base64.length % 4)) % 4);
		const json = atob(padded);
		const parsed = JSON.parse(json) as { exp?: unknown };
		if (typeof parsed.exp !== "number") {
			throw new Error("JWT exp claim is missing or not a number.");
		}
		return parsed.exp * 1000;
	} catch {
		throw new Error("The server's response could not be understood.");
	}
}

/** Exchanges a WorkOS authorization code for tokens. PKCE: no client secret. */
export async function exchangeWorkOSCode(
	input: { clientId: string; code: string; codeVerifier: string },
	{ fetchImpl = fetch, now = Date.now }: Deps = {},
): Promise<CloudTokens> {
	const response = await fetchImpl("https://api.workos.com/user_management/authenticate", {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({
			client_id: input.clientId,
			grant_type: "authorization_code",
			code: input.code,
			code_verifier: input.codeVerifier,
		}),
	});
	if (!response.ok) {
		throw new Error(await errorMessage(response, `Sign-in failed (${response.status}).`));
	}
	const body = (await response.json()) as { access_token: string; refresh_token?: string };
	return {
		accessToken: body.access_token,
		refreshToken: body.refresh_token,
		expiresAt: decodeJwtExpiryMs(body.access_token),
	};
}

/** Rotates a WorkOS access/refresh token pair. Public client: no client secret. */
export async function refreshWorkOSTokens(
	input: { clientId: string; refreshToken: string },
	{ fetchImpl = fetch }: Deps = {},
): Promise<CloudTokens> {
	const response = await fetchImpl("https://api.workos.com/user_management/authenticate", {
		method: "POST",
		headers: { "content-type": "application/json" },
		body: JSON.stringify({
			client_id: input.clientId,
			grant_type: "refresh_token",
			refresh_token: input.refreshToken,
		}),
	});
	if (!response.ok) {
		let body: { error?: string; code?: string; message?: string } = {};
		try {
			body = await response.json();
		} catch {
			// HTTP 401 is definitive even when the response has no JSON body.
		}
		const message = body?.message || `Session refresh failed (${response.status}).`;
		if (response.status === 401 || (response.status === 400 && (body?.error === "invalid_grant" || body?.code === "invalid_grant"))) {
			throw new RefreshRejectedError(message);
		}
		throw new Error(message);
	}
	const body = (await response.json()) as { access_token: string; refresh_token: string };
	return {
		accessToken: body.access_token,
		refreshToken: body.refresh_token,
		expiresAt: decodeJwtExpiryMs(body.access_token),
	};
}
