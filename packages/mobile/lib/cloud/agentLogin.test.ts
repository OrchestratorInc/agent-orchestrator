import { describe, expect, it, vi } from "vitest";
import {
	claudeLoopbackRedirect,
	CODEX_LOOPBACK_REDIRECT,
	exchangeClaudeCode,
	finishCodexBrowserLogin,
	parseLoopbackCallback,
	startCodexBrowserLogin,
	CLAUDE_MANUAL_REDIRECT,
	CLAUDE_OAUTH_CLIENT_ID,
	CODEX_CLIENT_ID,
	finishClaudeLogin,
	finishCodexLogin,
	parseClaudeCode,
	startClaudeLogin,
	startCodexLogin,
	waitForCodexApproval,
} from "./agentLogin";

const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status });

describe("Claude Code sign-in", () => {
	const deps = { randomBytes: (count: number) => new Uint8Array(count).fill(250), sha256Base64: async () => "ab+/cd==" };

	it("asks Claude for a setup token with the manual redirect and PKCE", async () => {
		const login = await startClaudeLogin(deps);
		const url = new URL(login.authorizeUrl);
		expect(`${url.origin}${url.pathname}`).toBe("https://claude.com/cai/oauth/authorize");
		expect(Object.fromEntries(url.searchParams)).toMatchObject({
			code: "true", client_id: CLAUDE_OAUTH_CLIENT_ID, response_type: "code", redirect_uri: CLAUDE_MANUAL_REDIRECT,
			scope: "user:inference", code_challenge: "ab-_cd", code_challenge_method: "S256", state: login.state,
		});
		expect(login.verifier).toMatch(/^[A-Za-z0-9_-]{43}$/);
	});

	it("accepts code#state, a bare code, or the callback URL, and refuses another sign-in's code", () => {
		expect(parseClaudeCode(" abc#st ", "st")).toBe("abc");
		expect(parseClaudeCode("abc", "st")).toBe("abc");
		expect(parseClaudeCode(`${CLAUDE_MANUAL_REDIRECT}?code=xyz&state=st`, "st")).toBe("xyz");
		expect(() => parseClaudeCode("abc#other", "st")).toThrow(/different sign-in/);
		expect(() => parseClaudeCode("  ", "st")).toThrow(/Paste the code/);
	});

	it("exchanges the code for an sk-ant-oat token and rejects anything else", async () => {
		const login = await startClaudeLogin(deps);
		const fetch = vi.fn(async () => json({ access_token: "sk-ant-oat01-token" }));
		await expect(finishClaudeLogin(login, `code1#${login.state}`, { fetch: fetch as unknown as typeof globalThis.fetch })).resolves.toBe("sk-ant-oat01-token");
		const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("https://platform.claude.com/v1/oauth/token");
		expect(JSON.parse(String(init.body))).toMatchObject({ grant_type: "authorization_code", code: "code1", redirect_uri: CLAUDE_MANUAL_REDIRECT, code_verifier: login.verifier, state: login.state, expires_in: 31_536_000 });
		const bad = vi.fn(async () => json({ access_token: "sk-ant-api03-nope" }));
		await expect(finishClaudeLogin(login, "code1", { fetch: bad as unknown as typeof globalThis.fetch })).rejects.toThrow(/setup token/);
	});
});

describe("Codex (ChatGPT) device sign-in", () => {
	it("starts a device code with the Codex client", async () => {
		const fetch = vi.fn(async () => json({ device_auth_id: "dev1", usercode: "ABCD-1234", interval: "3" }));
		await expect(startCodexLogin({ fetch: fetch as unknown as typeof globalThis.fetch })).resolves.toEqual({
			deviceAuthId: "dev1", userCode: "ABCD-1234", intervalMs: 3000, verificationUrl: "https://auth.openai.com/codex/device",
		});
		const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("https://auth.openai.com/api/accounts/deviceauth/usercode");
		expect(JSON.parse(String(init.body))).toEqual({ client_id: CODEX_CLIENT_ID });
	});

	it("polls until approved, treating 403/404 as pending", async () => {
		const responses = [json({}, 403), json({}, 404), json({ authorization_code: "auth", code_challenge: "c", code_verifier: "v" })];
		const fetch = vi.fn(async () => responses.shift()!);
		const sleep = vi.fn(async () => {});
		await expect(waitForCodexApproval({ deviceAuthId: "d", userCode: "u", intervalMs: 10, verificationUrl: "" }, { fetch: fetch as unknown as typeof globalThis.fetch, sleep })).resolves.toEqual({ authorizationCode: "auth", codeVerifier: "v" });
		expect(sleep).toHaveBeenCalledTimes(2);
	});

	it("stops on a hard failure", async () => {
		const fetch = vi.fn(async () => json({}, 500));
		await expect(waitForCodexApproval({ deviceAuthId: "d", userCode: "u", intervalMs: 10, verificationUrl: "" }, { fetch: fetch as unknown as typeof globalThis.fetch, sleep: async () => {} })).rejects.toThrow(/500/);
	});

	it("writes the auth.json Codex itself writes", async () => {
		const claims = btoa(JSON.stringify({ "https://api.openai.com/auth": { chatgpt_account_id: "acct_1" } })).replace(/=+$/, "");
		const fetch = vi.fn(async () => json({ id_token: `h.${claims}.s`, access_token: "at", refresh_token: "rt" }));
		const auth = JSON.parse(await finishCodexLogin({ authorizationCode: "auth", codeVerifier: "v" }, { fetch: fetch as unknown as typeof globalThis.fetch }, () => new Date("2026-11-02T00:00:00Z")));
		expect(auth).toEqual({
			auth_mode: "chatgpt", OPENAI_API_KEY: null,
			tokens: { id_token: `h.${claims}.s`, access_token: "at", refresh_token: "rt", account_id: "acct_1" },
			last_refresh: "2026-11-02T00:00:00.000Z",
		});
		const [url, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(url).toBe("https://auth.openai.com/oauth/token");
		expect(String(init.body)).toContain("redirect_uri=https%3A%2F%2Fauth.openai.com%2Fdeviceauth%2Fcallback");
		expect(String(init.body)).toContain(`client_id=${CODEX_CLIENT_ID}`);
	});
});

describe("one-tap browser sign-in", () => {
	const deps = { randomBytes: (count: number) => new Uint8Array(count).fill(7), sha256Base64: async () => "q+/w==" };

	it("asks Claude for a setup token with the loopback redirect the desktop uses", async () => {
		const login = await startClaudeLogin(deps, claudeLoopbackRedirect(51234));
		expect(new URL(login.authorizeUrl).searchParams.get("redirect_uri")).toBe("http://localhost:51234/callback");
		const fetch = vi.fn(async () => json({ access_token: "sk-ant-oat01-x" }));
		await expect(exchangeClaudeCode(login, "c", { fetch: fetch as unknown as typeof globalThis.fetch })).resolves.toBe("sk-ant-oat01-x");
		const [, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(JSON.parse(String(init.body))).toMatchObject({ code: "c", redirect_uri: "http://localhost:51234/callback" });
	});

	it("authorizes Codex exactly as `codex login` does, on its registered loopback port", async () => {
		const login = await startCodexBrowserLogin(deps);
		const url = new URL(login.authorizeUrl);
		expect(`${url.origin}${url.pathname}`).toBe("https://auth.openai.com/oauth/authorize");
		expect(Object.fromEntries(url.searchParams)).toMatchObject({
			response_type: "code", client_id: CODEX_CLIENT_ID, redirect_uri: CODEX_LOOPBACK_REDIRECT, code_challenge: "q-_w", code_challenge_method: "S256",
			id_token_add_organizations: "true", codex_cli_simplified_flow: "true", originator: "codex_cli_rs", state: login.state,
		});
		expect(CODEX_LOOPBACK_REDIRECT).toBe("http://localhost:1455/auth/callback");
		const fetch = vi.fn(async () => json({ id_token: "h.e30.s", access_token: "at", refresh_token: "rt" }));
		const auth = JSON.parse(await finishCodexBrowserLogin(login, "code9", { fetch: fetch as unknown as typeof globalThis.fetch }, () => new Date(0)));
		expect(auth.tokens).toMatchObject({ access_token: "at", refresh_token: "rt" });
		const [, init] = fetch.mock.calls[0] as unknown as [string, RequestInit];
		expect(String(init.body)).toContain(`redirect_uri=${encodeURIComponent(CODEX_LOOPBACK_REDIRECT)}`);
		expect(String(init.body)).toContain(`code_verifier=${login.verifier}`);
	});

	it("reads the code from the loopback redirect and refuses a foreign or failed one", () => {
		expect(parseLoopbackCallback("/auth/callback?code=abc%2Fd&state=s1", "s1")).toBe("abc/d");
		expect(() => parseLoopbackCallback("/auth/callback?code=abc&state=other", "s1")).toThrow(/verified/);
		expect(() => parseLoopbackCallback("/callback?error=access_denied&state=s1", "s1")).toThrow(/cancelled/);
		expect(() => parseLoopbackCallback("/callback?state=s1", "s1")).toThrow(/did not return a code/);
	});
});
