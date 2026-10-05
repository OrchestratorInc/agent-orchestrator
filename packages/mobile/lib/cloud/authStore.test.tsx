import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const native = vi.hoisted(() => ({
	getItemAsync: vi.fn(), setItemAsync: vi.fn(), deleteItemAsync: vi.fn(),
	openAuthSessionAsync: vi.fn(),
	listeners: new Set<(state: string) => void>(),
}));
vi.mock("expo-secure-store", () => native);
vi.mock("expo-web-browser", () => native);
vi.mock("expo-crypto", () => ({
	getRandomBytes: () => new Uint8Array(32),
	getRandomBytesAsync: async () => new Uint8Array(32),
	digestStringAsync: async () => "digest",
	CryptoDigestAlgorithm: { SHA256: "SHA256" }, CryptoEncoding: { BASE64: "base64" },
}));
vi.mock("react-native", () => ({
	AppState: {
		addEventListener: (_event: string, listener: (state: string) => void) => {
			native.listeners.add(listener);
			return { remove: () => native.listeners.delete(listener) };
		},
	},
}));

import { CloudAuthProvider, useCloudAuth, type CloudAuthState } from "./authStore";

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}
const org = { id: "same-org", slug: "ada", displayName: "Ada", role: "owner" };
const account = { user: { id: "ada", email: "ada@example.com", displayName: "Ada", authProvider: "local" }, organizations: [org] };
const newToken = `header.${btoa(JSON.stringify({ exp: 4_000_000_000 }))}.sig`;
const unauthorized = () => new Response(JSON.stringify({ error: "Unauthorized", code: "AUTH_REQUIRED", message: "Expired", requestId: "test" }), { status: 401 });
const json = (value: unknown, status = 200) => new Response(JSON.stringify(value), { status });

let raw: string | null;
let auth: CloudAuthState;
let renderer: ReactTestRenderer | undefined;
let fetchImpl: ReturnType<typeof vi.fn<typeof fetch>>;

function Probe() {
	auth = useCloudAuth();
	return null;
}
async function mount() {
	await act(async () => { renderer = create(<CloudAuthProvider baseUrl="http://127.0.0.1:8081"><Probe /></CloudAuthProvider>); });
}
async function replaceSession(method: "local" | "register" | "hosted") {
	await act(async () => {
		if (method === "local") await auth.signInLocal("ada@example.com", "password");
		else if (method === "register") await auth.registerLocal({ email: "ada@example.com", password: "password", displayName: "Ada", orgSlug: "ada", orgName: "Ada" });
		else await auth.signInWithWorkOS();
	});
}

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	// React 19 emits a deprecation notice on every renderer creation; keep all
	// other console errors visible, including missing act() boundaries.
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
	raw = JSON.stringify({ accessToken: "old", expiresAt: 4_000_000_000_000 });
	native.getItemAsync.mockReset().mockImplementation(async () => raw);
	native.setItemAsync.mockReset().mockImplementation(async (_key, value) => { raw = value; });
	native.deleteItemAsync.mockReset().mockImplementation(async () => { raw = null; });
	native.openAuthSessionAsync.mockReset().mockImplementation(async (url: string) => ({
		type: "success", url: `aoagents.ao://callback?code=code&state=${new URL(url).searchParams.get("state")}`,
	}));
	fetchImpl = vi.fn<typeof fetch>(async (url) => {
		if (String(url).endsWith("/me")) return json(account);
		if (String(url).endsWith("/authenticate")) return json({ access_token: newToken, refresh_token: "new-refresh" });
		if (/\/auth\/local\/(login|register)$/.test(String(url))) return json({ token: newToken, expiresAt: new Date(4_000_000_000_000).toISOString() });
		throw new Error(`Unexpected request ${url}`);
	});
	vi.stubGlobal("fetch", fetchImpl);
});
afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("CloudAuthProvider session boundaries", () => {
	it("reuses the browser identity when signing in after sign-out", async () => {
		await mount();
		await act(async () => { await auth.signOut(); });
		expect(raw).toBeNull();
		await replaceSession("hosted");
		expect(native.openAuthSessionAsync).toHaveBeenCalledWith(
			expect.any(String),
			expect.any(String),
		);
		expect(auth.signedIn).toBe(true);
	});

	it("asks AuthKit to show the login screen after sign-out", async () => {
		await mount();
		await act(async () => { await auth.signOut(); });
		await replaceSession("hosted");
		const authorizationUrl = new URL(native.openAuthSessionAsync.mock.calls[0]![0] as string);
		expect(authorizationUrl.searchParams.get("prompt")).toBe("login");
	});

	it("keeps replacement credentials when an earlier refresh storage write finishes late", async () => {
		raw = JSON.stringify({ accessToken: "expired", refreshToken: "old-refresh", expiresAt: 0 });
		const write = deferred<void>();
		native.setItemAsync.mockImplementationOnce(async (_key, value) => { await write.promise; raw = value; });
		await mount();
		expect(native.setItemAsync).toHaveBeenCalledOnce();
		let replacement!: Promise<void>;
		await act(async () => { replacement = auth.signInLocal("ada@example.com", "password"); });
		await act(async () => { write.resolve(); await replacement; });
		expect(JSON.parse(raw!).accessToken).toBe(newToken);
		expect(JSON.parse(raw!).refreshToken).toBeUndefined();
		expect(auth.signedIn).toBe(true);
	});

	it("keeps a replacement account in the same org when an old request returns 401", async () => {
		await mount();
		const response = deferred<Response>();
		fetchImpl.mockImplementationOnce(() => response.promise);
		const pending = auth.client.getCurrentAccount().catch((error: unknown) => error);
		await act(async () => { await Promise.resolve(); });
		await replaceSession("local");
		await act(async () => { response.resolve(unauthorized()); await pending; });
		expect(auth.signedIn).toBe(true);
		expect(auth.orgId).toBe("same-org");
		expect(JSON.parse(raw!).accessToken).toBe(newToken);
	});

	it("invalidates the current session after its own request returns 401", async () => {
		await mount();
		fetchImpl.mockImplementationOnce(async () => unauthorized());
		await act(async () => { await expect(auth.client.getCurrentAccount()).rejects.toThrow("Expired"); });
		expect(auth.signedIn).toBe(false);
		expect(auth.orgId).toBeNull();
		expect(raw).toBeNull();
	});

	it.each(["local", "register", "hosted"] as const)("discards a pending credential read when %s sign-in replaces the session", async (method) => {
		await mount();
		const oldRaw = raw;
		const read = deferred<string | null>();
		native.getItemAsync.mockImplementationOnce(() => read.promise);
		const pending = auth.client.getCurrentAccount().then(() => "sent", () => "discarded");
		await replaceSession(method);
		fetchImpl.mockClear();
		await act(async () => { read.resolve(oldRaw); await pending; });
		expect(await pending).toBe("discarded");
		expect(fetchImpl).not.toHaveBeenCalled();
		expect(auth.signedIn).toBe(true);
		expect(JSON.parse(raw!).accessToken).toBe(newToken);
	});
});

describe("CloudAuthProvider organization recovery", () => {
	it("does not create an organization from a previous account's late lookup", async () => {
		const lookup = deferred<Response>();
		fetchImpl.mockImplementationOnce(() => lookup.promise);
		await mount();
		await replaceSession("local");
		fetchImpl.mockClear();
		await act(async () => { lookup.resolve(json({ ...account, organizations: [] })); });
		expect(fetchImpl).not.toHaveBeenCalled();
		expect(auth.orgId).toBe("same-org");
	});

	it("recovers an organization-creation failure when the app returns to the foreground", async () => {
		fetchImpl.mockResolvedValueOnce(json({ ...account, organizations: [] }))
			.mockRejectedValueOnce(new Error("Workspace service unavailable"));
		await mount();
		expect(auth.orgError).toBe("Workspace service unavailable");
		expect(auth.signedIn).toBe(true);
		await act(async () => { for (const listener of native.listeners) listener("active"); });
		expect(auth.orgId).toBe("same-org");
		expect(auth.orgError).toBeNull();
		expect(JSON.parse(raw!).accessToken).toBe("old");
	});

	it("exposes initial resolution failure and recovers on retry without signing in", async () => {
		fetchImpl.mockRejectedValueOnce(new Error("Offline"));
		await mount();
		expect(auth.signedIn).toBe(true);
		expect(auth.orgId).toBeNull();
		expect(auth.orgError).toBe("Offline");
		expect(auth.orgLoading).toBe(false);
		await act(async () => { await auth.retryOrgResolution(); });
		expect(auth.orgId).toBe("same-org");
		expect(auth.orgError).toBeNull();
		expect(JSON.parse(raw!).accessToken).toBe("old");
	});

	it("coalesces overlapping retries so an account gets only one new organization", async () => {
		fetchImpl.mockRejectedValueOnce(new Error("Offline"));
		await mount();
		expect(auth.orgError).toBe("Offline");
		const created = deferred<Response>();
		let creations = 0;
		fetchImpl.mockImplementation(async (url) => {
			if (String(url).endsWith("/me")) return json({ ...account, organizations: [] });
			if (String(url).endsWith("/orgs")) { creations += 1; return created.promise; }
			throw new Error(`Unexpected request ${url}`);
		});
		let first!: Promise<void>;
		let second!: Promise<void>;
		await act(async () => { first = auth.retryOrgResolution(); second = auth.retryOrgResolution(); });
		expect(auth.orgLoading).toBe(true);
		expect(creations).toBe(1);
		await act(async () => { created.resolve(json({ organization: org })); await Promise.all([first, second]); });
		expect(auth.orgId).toBe("same-org");
		expect(auth.orgError).toBeNull();
		expect(auth.orgLoading).toBe(false);
	});
});
