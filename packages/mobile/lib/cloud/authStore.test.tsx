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
vi.mock("../accountHosts", () => ({ cancelAccountHostSync: vi.fn() }));
vi.mock("react-native", () => ({
	AppState: {
		addEventListener: (_event: string, listener: (state: string) => void) => {
			native.listeners.add(listener);
			return { remove: () => native.listeners.delete(listener) };
		},
	},
}));

import { CloudAuthProvider, useCloudAuth, type CloudAuthState } from "./authStore";
import { createAccountSessionOwner } from "./sessionOwner";
import { createTokenProvider } from "./session";
import { readTokens, writeTokens, clearTokens } from "./tokens";
import { createMobileCloudClient } from "./client";
import { resolveCloudDeployment } from "./deployment";

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}
const org = { id: "same-org", slug: "ada", displayName: "Ada", role: "owner" };
const account = { user: { id: "ada", email: "ada@example.com", displayName: "Ada", authProvider: "local" }, organizations: [org] };
const newToken = `header.${btoa(JSON.stringify({ exp: 4_000_000_000 }))}.sig`;
const unauthorized = () => new Response(JSON.stringify({ error: "Unauthorized", code: "AUTH_REQUIRED", message: "Expired", requestId: "test" }), { status: 401 });
const forbidden = () => new Response(JSON.stringify({ error: "Forbidden", code: "forbidden", message: "Membership changed", requestId: "test" }), { status: 403 });
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
	const deployment = resolveCloudDeployment({ cloudUrl: "http://127.0.0.1:8081", development: true });
	const tokens = createTokenProvider({ read: () => readTokens(deployment.key), write: (value) => writeTokens(value, deployment.key), clear: () => clearTokens(deployment.key), refresh: async () => ({ accessToken: newToken, refreshToken: "new-refresh", expiresAt: 4_000_000_000_000 }), now: Date.now });
	const owner = createAccountSessionOwner({ tokens, readAccount: async () => {
		const { user } = await createMobileCloudClient({ baseUrl: deployment.baseUrl, tokens }).getCurrentAccount();
		return { id: user.id, email: user.email };
	} });
	await act(async () => { renderer = create(<CloudAuthProvider owner={owner} baseUrl="http://127.0.0.1:8081"><Probe /></CloudAuthProvider>); });
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
	raw = JSON.stringify({ version: 2, deploymentKey: resolveCloudDeployment({ cloudUrl: "http://127.0.0.1:8081", development: true }).key, tokens: { accessToken: "old", expiresAt: 4_000_000_000_000 } });
	native.getItemAsync.mockReset().mockImplementation(async () => raw);
	native.setItemAsync.mockReset().mockImplementation(async (_key, value) => { raw = value; });
	native.deleteItemAsync.mockReset().mockImplementation(async (key: string) => { if (key.startsWith("ao.cloud.tokens.v2.")) raw = null; });
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
	it("restores the existing workspace when a new sign-in browser is dismissed", async () => {
		await mount();
		expect(auth.orgId).toBe("same-org");
		const browser = deferred<{ type: string }>();
		native.openAuthSessionAsync.mockImplementationOnce(() => browser.promise);
		let pending!: Promise<boolean>;
		await act(async () => { pending = auth.signInWithWorkOS(); });
		expect(auth.orgId).toBeNull();
		await act(async () => { browser.resolve({ type: "cancel" }); await pending; });
		expect(auth.orgId).toBe("same-org");
		expect(auth.signedIn).toBe(true);
	});
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
		raw = JSON.stringify({ version: 2, deploymentKey: resolveCloudDeployment({ cloudUrl: "http://127.0.0.1:8081", development: true }).key, tokens: { accessToken: "expired", refreshToken: "old-refresh", expiresAt: 0 } });
		const write = deferred<void>();
		native.setItemAsync.mockImplementationOnce(async (_key, value) => { await write.promise; raw = value; });
		await mount();
		expect(native.setItemAsync).toHaveBeenCalledOnce();
		let replacement!: Promise<void>;
		await act(async () => { replacement = auth.signInLocal("ada@example.com", "password"); });
		await act(async () => { write.resolve(); await replacement; });
		expect(JSON.parse(raw!).tokens.accessToken).toBe(newToken);
		expect(JSON.parse(raw!).tokens.refreshToken).toBeUndefined();
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
		expect(JSON.parse(raw!).tokens.accessToken).toBe(newToken);
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
		expect(JSON.parse(raw!).tokens.accessToken).toBe(newToken);
	});
});

describe("CloudAuthProvider organization recovery", () => {
	it("adopts the current workspace after an org-scoped refusal without replaying the rejected action", async () => {
		await mount();
		const epoch = auth.sessionEpoch;
		const requests: string[] = [];
		fetchImpl.mockImplementation(async (url) => {
			requests.push(String(url));
			if (String(url).endsWith("/projects")) return forbidden();
			if (String(url).endsWith("/me")) return json({ ...account, activeOrganizationId: "new-org", organizations: [{ ...org, id: "new-org" }], offering: { kind: "b2c", providers: ["freestyle"], defaultProvider: "freestyle", features: [], billingMode: "self_serve" } });
			throw new Error(`Unexpected request ${url}`);
		});
		await act(async () => { await expect(auth.client.listProjects("same-org")).rejects.toThrow("Membership changed"); });
		expect(auth.orgId).toBe("new-org");
		expect(auth.offering?.defaultProvider).toBe("freestyle");
		expect(auth.signedIn).toBe(true); expect(auth.sessionEpoch).toBe(epoch);
		expect(requests).toEqual([
			"http://127.0.0.1:8081/api/cloud/v1/orgs/same-org/projects",
			"http://127.0.0.1:8081/api/cloud/v1/me",
		]);
		expect(JSON.parse(raw!).tokens.accessToken).toBe("old");
	});
	it("coalesces forbidden responses and keeps cached data while membership is revalidated", async () => {
		await mount();
		const lookup = deferred<Response>();
		let lookups = 0;
		fetchImpl.mockImplementation(async (url) => {
			if (String(url).endsWith("/me")) { lookups++; return lookup.promise; }
			return forbidden();
		});
		await act(async () => {
			await Promise.all([auth.client.listProjects("same-org"), auth.client.listSessions("same-org")].map((request) => request.catch(() => {})));
		});
		expect(lookups).toBe(1);
		expect(auth.orgId).toBe("same-org"); expect(auth.orgLoading).toBe(false);
		await act(async () => { lookup.resolve(json({ ...account, activeOrganizationId: "new-org", organizations: [{ ...org, id: "new-org" }] })); });
		expect(auth.orgId).toBe("new-org");
	});
	it("does not replay a rejected mutation during membership recovery", async () => {
		await mount(); const requests: Array<{ url: string; method: string | undefined }> = [];
		fetchImpl.mockImplementation(async (url, init) => {
			requests.push({ url: String(url), method: init?.method });
			if (String(url).endsWith("/me")) return json(account);
			return forbidden();
		});
		await act(async () => { await expect(auth.client.createProject("same-org", { displayName: "Draft", repositoryUrl: "https://github.com/example/repo", defaultBranch: "main" }, { idempotencyKey: "draft-key" })).rejects.toThrow("Membership changed"); });
		expect(requests.filter((request) => request.method === "POST")).toEqual([{ url: "http://127.0.0.1:8081/api/cloud/v1/orgs/same-org/projects", method: "POST" }]);
		expect(requests.filter((request) => request.url.endsWith("/me"))).toHaveLength(1);
		expect(auth.orgId).toBe("same-org"); expect(auth.signedIn).toBe(true);
	});
	it.each(["provider_forbidden", "WORKSPACE_READ_ONLY"])("does not treat %s as a membership change", async (code) => {
		await mount(); fetchImpl.mockClear().mockImplementation(async () => json({ error: "Forbidden", code, message: "Policy denied", requestId: "test" }, 403));
		await act(async () => { await expect(auth.client.listProjects("same-org")).rejects.toThrow("Policy denied"); });
		expect(fetchImpl).toHaveBeenCalledOnce(); expect(auth.orgId).toBe("same-org"); expect(auth.signedIn).toBe(true);
	});
	it("retains the workspace and offers recovery when membership revalidation is offline", async () => {
		await mount();
		fetchImpl.mockImplementation(async (url) => {
			if (String(url).endsWith("/me")) throw new Error("Offline");
			return forbidden();
		});
		await act(async () => { await auth.client.listProjects("same-org").catch(() => {}); });
		expect(auth.orgId).toBe("same-org"); expect(auth.orgError).toBe("Offline"); expect(auth.signedIn).toBe(true);
		fetchImpl.mockImplementation(async () => json(account));
		await act(async () => { await auth.retryOrgResolution(); });
		expect(auth.orgError).toBeNull();
	});
	it("does not recursively retry a forbidden membership lookup", async () => {
		await mount(); fetchImpl.mockClear().mockImplementation(async () => forbidden());
		await act(async () => { await auth.client.listProjects("same-org").catch(() => {}); });
		expect(fetchImpl).toHaveBeenCalledTimes(2);
		expect(auth.orgError).toBe("Membership changed"); expect(auth.signedIn).toBe(true);
	});
	it("ignores a former login's late org refusal", async () => {
		await mount(); const response = deferred<Response>();
		fetchImpl.mockImplementationOnce(() => response.promise);
		const oldRequest = auth.client.listProjects("same-org").catch(() => {});
		await act(async () => { await Promise.resolve(); });
		await replaceSession("local"); fetchImpl.mockClear();
		await act(async () => { response.resolve(forbidden()); await oldRequest; });
		expect(fetchImpl).not.toHaveBeenCalled(); expect(auth.orgId).toBe("same-org"); expect(auth.signedIn).toBe(true);
	});
	it("does not start a membership lookup after the provider unmounts", async () => {
		await mount(); const response = deferred<Response>();
		fetchImpl.mockImplementationOnce(() => response.promise);
		const oldRequest = auth.client.listProjects("same-org").catch(() => {});
		await act(async () => { await Promise.resolve(); renderer?.unmount(); });
		renderer = undefined; fetchImpl.mockClear();
		await act(async () => { response.resolve(forbidden()); await oldRequest; });
		expect(fetchImpl).not.toHaveBeenCalled();
	});
	it("refreshes a resolved workspace on foreground while retaining cached identity and coalescing reads", async () => {
		await mount();
		const client = auth.client;
		const lookup = deferred<Response>();
		fetchImpl.mockClear().mockImplementationOnce(() => lookup.promise);
		await act(async () => { for (const listener of native.listeners) { listener("active"); listener("active"); } });
		expect(fetchImpl).toHaveBeenCalledOnce();
		expect(auth.orgId).toBe("same-org"); expect(auth.orgLoading).toBe(false);
		expect(auth.client).toBe(client);
		await act(async () => { lookup.resolve(json({ ...account, sandboxProviders: { available: ["freestyle", "coder"], default: "coder" } })); });
		expect(auth.offering?.defaultProvider).toBe("coder");
		expect(auth.client).toBe(client);
		fetchImpl.mockRejectedValueOnce(new Error("Offline"));
		await act(async () => { for (const listener of native.listeners) listener("active"); });
		expect(auth.orgId).toBe("same-org"); expect(auth.offering?.defaultProvider).toBe("coder");
		expect(auth.orgError).toBe("Offline"); expect(auth.orgLoading).toBe(false);
		await act(async () => { for (const listener of native.listeners) listener("active"); });
		expect(auth.orgError).toBeNull();
	});
	it("adopts an active organization change but ignores a former account's late foreground response", async () => {
		await mount();
		fetchImpl.mockResolvedValueOnce(json({ ...account, activeOrganizationId: "new-org", organizations: [org, { ...org, id: "new-org" }] }));
		await act(async () => { for (const listener of native.listeners) listener("active"); });
		expect(auth.orgId).toBe("new-org");
		const lookup = deferred<Response>(); fetchImpl.mockImplementationOnce(() => lookup.promise);
		await act(async () => { for (const listener of native.listeners) listener("active"); });
		await act(async () => { await auth.signOut(); });
		await act(async () => { lookup.resolve(json(account)); });
		expect(auth.orgId).toBeNull(); expect(auth.offering).toBeNull(); expect(auth.signedIn).toBe(false);
	});
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
		expect(JSON.parse(raw!).tokens.accessToken).toBe("old");
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
		expect(JSON.parse(raw!).tokens.accessToken).toBe("old");
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
