import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("expo-secure-store", () => ({ getItemAsync: async () => null, setItemAsync: async () => {}, deleteItemAsync: async () => {} }));
vi.mock("expo-crypto", () => ({
	getRandomBytes: () => new Uint8Array(32).fill(1),
	getRandomBytesAsync: async () => new Uint8Array(32).fill(2),
	digestStringAsync: async () => "c3ludGhldGljLWNoYWxsZW5nZQ==",
	CryptoDigestAlgorithm: { SHA256: "SHA-256" },
	CryptoEncoding: { BASE64: "base64" },
}));
vi.mock("expo-web-browser", () => ({ openAuthSessionAsync: vi.fn() }));

import * as WebBrowser from "expo-web-browser";
import { createAccountSessionOwner, signInToDeployment } from "./sessionOwner";
import { resolveCloudDeployment } from "./deployment";
import { createTokenProvider } from "./session";
import type { CloudTokens } from "./tokens";

afterEach(() => { vi.unstubAllGlobals(); vi.clearAllMocks(); });

describe("deployment sign-in", () => {
	it("authorizes through the production HTTPS return page and accepts the native bounce", async () => {
		let stored: CloudTokens | null = null;
		const tokens = createTokenProvider({ read: async () => stored, write: async (value) => { stored = value; }, clear: async () => { stored = null; }, refresh: async () => { throw new Error("Unexpected refresh"); }, now: Date.now });
		const owner = createAccountSessionOwner({ tokens, readAccount: async () => ({ id: "user-1", email: "one@example.test" }) });
		vi.mocked(WebBrowser.openAuthSessionAsync).mockImplementation(async (url, returnUrl) => {
			const auth = new URL(url);
			expect(auth.searchParams.get("client_id")).toBe("client_01KZ3VRKTDKBZ3JA8AMKTF6W2X");
			expect(auth.searchParams.get("redirect_uri")).toBe("https://api.aoagents.dev/app/auth/return");
			expect(auth.searchParams.get("code_challenge_method")).toBe("S256");
			expect(returnUrl).toBe("ao-app://callback");
			return { type: "success", url: `ao-app://callback?code=test-code&state=${auth.searchParams.get("state")}` };
		});
		const accessToken = `header.${btoa(JSON.stringify({ exp: 4_000_000_000 }))}.signature`;
		vi.stubGlobal("fetch", async (url: string, init: RequestInit) => {
			expect(url).toBe("https://api.workos.com/user_management/authenticate");
			expect(JSON.parse(String(init.body))).toEqual({ client_id: "client_01KZ3VRKTDKBZ3JA8AMKTF6W2X", grant_type: "authorization_code", code: "test-code", code_verifier: "AgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI" });
			return new Response(JSON.stringify({ access_token: accessToken, refresh_token: "test-refresh" }), { status: 200 });
		});
		expect(await signInToDeployment(resolveCloudDeployment({ cloudUrl: "https://api.aoagents.dev", development: true }), owner)).toBe(true);
		expect(await owner.getToken()).toBe(accessToken);
		expect(owner.getSnapshot()).toMatchObject({ busy: false, signedIn: true, account: { id: "user-1" }, error: null });
	});
});
