// Coding-agent sign-ins a phone can complete on its own.
//
// The preferred path is the one the desktop and the CLIs use: PKCE in the
// browser, finishing on a http://localhost redirect that the app catches with
// its own loopback listener (native/oauth-loopback). The browser is the
// system sign-in sheet, which shares the person's existing sessions, so a
// signed-in person only taps Allow.
//
// Fallbacks, for a phone where the listener cannot run:
// - Claude Code: Claude's manual redirect, which shows a code to paste back
//   (what `claude setup-token` does in a terminal without a browser).
// - Codex: ChatGPT's device-code sign-in (`codex login --device-auth`).
//
// Claude yields the long-lived `sk-ant-oat01-…` setup token; Codex yields the
// auth.json the CLI would write. Both are what Orchestrator Cloud stores.
//
// Either result is uploaded with PUT /me/providers/{agent}; the control plane
// validates it against the provider before storing it. Nothing is kept on the
// phone.

export type LoginDeps = {
	fetch: typeof fetch;
	randomBytes: (count: number) => Uint8Array;
	/** SHA-256 of a UTF-8 string, as standard base64. */
	sha256Base64: (input: string) => Promise<string>;
	sleep: (ms: number) => Promise<void>;
};

function base64Url(bytes: Uint8Array): string {
	let binary = "";
	for (const byte of bytes) binary += String.fromCharCode(byte);
	return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function base64ToUrl(base64: string): string {
	return base64.replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

// ---- Claude Code ------------------------------------------------------------

// Claude Code's public OAuth client, mirrored from the claude binary as the
// desktop does (provider-auth-flow.ts). The manual redirect is the CLI's
// MANUAL_REDIRECT_URL: Claude's page displays `code#state` for pasting.
export const CLAUDE_OAUTH_CLIENT_ID = "9d1c250a-e61b-44d9-88ed-5944d1962f5e";
export const CLAUDE_AUTHORIZE_URL = "https://claude.com/cai/oauth/authorize";
export const CLAUDE_TOKEN_URL = "https://platform.claude.com/v1/oauth/token";
export const CLAUDE_MANUAL_REDIRECT = "https://platform.claude.com/oauth/code/callback";
const CLAUDE_SCOPE = "user:inference";
const CLAUDE_TOKEN_TTL_SECONDS = 31_536_000;

export type ClaudeLogin = { authorizeUrl: string; verifier: string; state: string; redirectUri: string };

/** Claude's loopback redirect, as the desktop and the CLI register it: any port, /callback. */
export const CLAUDE_LOOPBACK_PATH = "/callback";
export function claudeLoopbackRedirect(port: number): string {
	return `http://localhost:${port}${CLAUDE_LOOPBACK_PATH}`;
}

export async function startClaudeLogin(deps: Pick<LoginDeps, "randomBytes" | "sha256Base64">, redirectUri: string = CLAUDE_MANUAL_REDIRECT): Promise<ClaudeLogin> {
	const verifier = base64Url(deps.randomBytes(32));
	const challenge = base64ToUrl(await deps.sha256Base64(verifier));
	const state = base64Url(deps.randomBytes(32));
	const params: Record<string, string> = {
		code: "true",
		client_id: CLAUDE_OAUTH_CLIENT_ID,
		response_type: "code",
		redirect_uri: redirectUri,
		scope: CLAUDE_SCOPE,
		code_challenge: challenge,
		code_challenge_method: "S256",
		state,
	};
	const query = Object.entries(params).map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).join("&");
	return { authorizeUrl: `${CLAUDE_AUTHORIZE_URL}?${query}`, verifier, state, redirectUri };
}

/**
 * Claude's page shows `code#state`. Accept that, a bare code, or the whole
 * callback URL someone copied from the address bar.
 */
export function parseClaudeCode(pasted: string, expectedState: string): string {
	let value = pasted.trim();
	if (!value) throw new Error("Paste the code Claude showed you.");
	if (/^https?:\/\//i.test(value)) {
		const query = value.split("?")[1]?.split("#")[0] ?? "";
		const params = new Map(query.split("&").map((pair) => pair.split("=").map(decodeURIComponent) as [string, string]));
		const code = params.get("code");
		const state = params.get("state");
		if (!code) throw new Error("That link has no code in it. Copy the code Claude shows instead.");
		value = state ? `${code}#${state}` : code;
	}
	const [code, state] = value.split("#");
	if (!code) throw new Error("Paste the code Claude showed you.");
	if (state && state !== expectedState) throw new Error("That code belongs to a different sign-in. Start again and paste the new code.");
	return code;
}

export async function finishClaudeLogin(login: ClaudeLogin, pasted: string, deps: Pick<LoginDeps, "fetch">): Promise<string> {
	return exchangeClaudeCode(login, parseClaudeCode(pasted, login.state), deps);
}

/** Exchange an authorization code for the setup token; the redirect must match the authorize request's. */
export async function exchangeClaudeCode(login: ClaudeLogin, code: string, deps: Pick<LoginDeps, "fetch">): Promise<string> {
	const response = await deps.fetch(CLAUDE_TOKEN_URL, {
		method: "POST",
		headers: { "Content-Type": "application/json", Accept: "application/json" },
		body: JSON.stringify({
			grant_type: "authorization_code",
			code,
			redirect_uri: login.redirectUri,
			client_id: CLAUDE_OAUTH_CLIENT_ID,
			code_verifier: login.verifier,
			state: login.state,
			expires_in: CLAUDE_TOKEN_TTL_SECONDS,
		}),
	});
	if (!response.ok) {
		throw new Error(response.status === 400 ? "Claude didn't accept that code. Codes expire quickly; start again." : `Claude sign-in failed (${response.status}).`);
	}
	const body = await response.json() as Record<string, unknown>;
	const token = body.access_token;
	if (typeof token !== "string" || !token.startsWith("sk-ant-oat")) throw new Error("Claude did not return a setup token.");
	return token;
}

// ---- Codex (ChatGPT) --------------------------------------------------------

// The Codex CLI's OAuth client and ChatGPT's device-code endpoints, as the
// codex binary uses them (verified against codex-cli 0.162.1).
export const CODEX_CLIENT_ID = "app_EMoamEEZ73f0CkXaXp7hrann";
export const CODEX_ISSUER = "https://auth.openai.com";
export const CODEX_DEVICE_PAGE = `${CODEX_ISSUER}/codex/device`;
const CODEX_DEVICE_TIMEOUT_MS = 15 * 60_000;

/**
 * Codex's browser sign-in redirect. The Codex OAuth client registers this one
 * loopback port; the CLI's own login server listens on it.
 */
export const CODEX_LOOPBACK_PORT = 1455;
export const CODEX_LOOPBACK_PATH = "/auth/callback";
export const CODEX_LOOPBACK_REDIRECT = `http://localhost:${CODEX_LOOPBACK_PORT}${CODEX_LOOPBACK_PATH}`;
const CODEX_SCOPE = "openid profile email offline_access api.connectors.read api.connectors.invoke";

export type CodexBrowserLogin = { authorizeUrl: string; verifier: string; state: string; redirectUri: string };

/** The authorization Codex's `codex login` opens, with its PKCE pair. */
export async function startCodexBrowserLogin(deps: Pick<LoginDeps, "randomBytes" | "sha256Base64">, redirectUri: string = CODEX_LOOPBACK_REDIRECT): Promise<CodexBrowserLogin> {
	const verifier = base64Url(deps.randomBytes(32));
	const challenge = base64ToUrl(await deps.sha256Base64(verifier));
	const state = base64Url(deps.randomBytes(32));
	const params: Record<string, string> = {
		response_type: "code",
		client_id: CODEX_CLIENT_ID,
		redirect_uri: redirectUri,
		scope: CODEX_SCOPE,
		code_challenge: challenge,
		code_challenge_method: "S256",
		id_token_add_organizations: "true",
		codex_cli_simplified_flow: "true",
		state,
		originator: "codex_cli_rs",
	};
	const query = Object.entries(params).map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).join("&");
	return { authorizeUrl: `${CODEX_ISSUER}/oauth/authorize?${query}`, verifier, state, redirectUri };
}

/**
 * The code from a loopback redirect's request target (`/path?code=…&state=…`),
 * checked against the sign-in that asked for it.
 */
export function parseLoopbackCallback(target: string, expectedState: string): string {
	const query = target.split("?")[1]?.split("#")[0] ?? "";
	const params = new Map(query.split("&").filter(Boolean).map((pair) => {
		const [key, ...rest] = pair.split("=");
		return [decodeURIComponent(key), decodeURIComponent(rest.join("=").replace(/\+/g, " "))] as [string, string];
	}));
	const error = params.get("error");
	if (error) throw new Error(params.get("error_description") || (error === "access_denied" ? "Sign-in was cancelled." : `Sign-in failed (${error}).`));
	if (params.get("state") !== expectedState) throw new Error("The sign-in response could not be verified. Start again.");
	const code = params.get("code");
	if (!code) throw new Error("The sign-in did not return a code. Start again.");
	return code;
}

export type CodexDeviceCode = { deviceAuthId: string; userCode: string; intervalMs: number; verificationUrl: string };

export async function startCodexLogin(deps: Pick<LoginDeps, "fetch">): Promise<CodexDeviceCode> {
	const response = await deps.fetch(`${CODEX_ISSUER}/api/accounts/deviceauth/usercode`, {
		method: "POST",
		headers: { "Content-Type": "application/json", Accept: "application/json" },
		body: JSON.stringify({ client_id: CODEX_CLIENT_ID }),
	});
	if (response.status === 404) throw new Error("ChatGPT device sign-in isn't available for this account. Turn on device code sign-in in ChatGPT's security settings, or use an API key.");
	if (!response.ok) throw new Error(`ChatGPT sign-in failed to start (${response.status}).`);
	const body = await response.json() as Record<string, unknown>;
	const deviceAuthId = body.device_auth_id;
	const userCode = body.user_code ?? body.usercode;
	if (typeof deviceAuthId !== "string" || typeof userCode !== "string") throw new Error("ChatGPT returned an unexpected sign-in code.");
	const interval = typeof body.interval === "number" ? body.interval : Number(body.interval);
	return { deviceAuthId, userCode, intervalMs: Math.max(1, Number.isFinite(interval) && interval > 0 ? interval : 5) * 1000, verificationUrl: CODEX_DEVICE_PAGE };
}

type CodexAuthorization = { authorizationCode: string; codeVerifier: string };

/** Poll until the person approves the code, the code expires, or the caller gives up. */
export async function waitForCodexApproval(device: CodexDeviceCode, deps: Pick<LoginDeps, "fetch" | "sleep">, signal?: AbortSignal): Promise<CodexAuthorization> {
	const deadline = Date.now() + CODEX_DEVICE_TIMEOUT_MS;
	while (Date.now() < deadline) {
		if (signal?.aborted) throw Object.assign(new Error("Sign-in cancelled."), { name: "AbortError" });
		const response = await deps.fetch(`${CODEX_ISSUER}/api/accounts/deviceauth/token`, {
			method: "POST",
			headers: { "Content-Type": "application/json", Accept: "application/json" },
			body: JSON.stringify({ device_auth_id: device.deviceAuthId, user_code: device.userCode }),
		});
		if (response.ok) {
			const body = await response.json() as Record<string, unknown>;
			if (typeof body.authorization_code !== "string" || typeof body.code_verifier !== "string") throw new Error("ChatGPT returned an unexpected approval.");
			return { authorizationCode: body.authorization_code, codeVerifier: body.code_verifier };
		}
		// 403/404 mean "not approved yet"; anything else is final.
		if (response.status !== 403 && response.status !== 404) throw new Error(`ChatGPT sign-in failed (${response.status}).`);
		await deps.sleep(device.intervalMs);
	}
	throw new Error("The ChatGPT code expired. Start again.");
}

function decodeJwtPayload(token: string): Record<string, unknown> {
	const part = token.split(".")[1];
	if (!part) return {};
	try {
		const padded = part.replace(/-/g, "+").replace(/_/g, "/").padEnd(Math.ceil(part.length / 4) * 4, "=");
		return JSON.parse(atob(padded)) as Record<string, unknown>;
	} catch {
		return {};
	}
}

/** Exchange the approval for tokens and write them as Codex's auth.json. */
export async function finishCodexLogin(approval: CodexAuthorization, deps: Pick<LoginDeps, "fetch">, now: () => Date = () => new Date(), redirectUri = `${CODEX_ISSUER}/deviceauth/callback`): Promise<string> {
	const form = [
		["grant_type", "authorization_code"],
		["code", approval.authorizationCode],
		["redirect_uri", redirectUri],
		["client_id", CODEX_CLIENT_ID],
		["code_verifier", approval.codeVerifier],
	].map(([key, value]) => `${encodeURIComponent(key)}=${encodeURIComponent(value)}`).join("&");
	const response = await deps.fetch(`${CODEX_ISSUER}/oauth/token`, {
		method: "POST",
		headers: { "Content-Type": "application/x-www-form-urlencoded", Accept: "application/json" },
		body: form,
	});
	if (!response.ok) throw new Error(`ChatGPT token exchange failed (${response.status}).`);
	const body = await response.json() as Record<string, unknown>;
	const { id_token: idToken, access_token: accessToken, refresh_token: refreshToken } = body;
	if (typeof idToken !== "string" || typeof accessToken !== "string" || typeof refreshToken !== "string") throw new Error("ChatGPT returned an incomplete sign-in.");
	const auth = decodeJwtPayload(idToken)["https://api.openai.com/auth"] as Record<string, unknown> | undefined;
	const accountId = typeof auth?.chatgpt_account_id === "string" ? auth.chatgpt_account_id : undefined;
	return JSON.stringify({
		auth_mode: "chatgpt",
		OPENAI_API_KEY: null,
		tokens: { id_token: idToken, access_token: accessToken, refresh_token: refreshToken, ...(accountId ? { account_id: accountId } : {}) },
		last_refresh: now().toISOString(),
	}, null, 2);
}

/** Finish Codex's browser sign-in: the loopback code, exchanged as the CLI does, written as auth.json. */
export function finishCodexBrowserLogin(login: CodexBrowserLogin, code: string, deps: Pick<LoginDeps, "fetch">, now: () => Date = () => new Date()): Promise<string> {
	return finishCodexLogin({ authorizationCode: code, codeVerifier: login.verifier }, deps, now, login.redirectUri);
}
