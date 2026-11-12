import { describe, expect, it, vi } from "vitest";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { computerName, registerRemotesIpc, remotesFilePath, type RemotesIpcDeps } from "./remotes-main";
import { RemoteRegistry } from "./remote-registry";
import { setPasswordCipher } from "./remotes-store";
import { createLocalHostIdentity, type RemoteHealth } from "./remote-request";
import { ExpectedIpcError, installIpcErrorCapture, resetMainErrorCaptureForTests, setMainErrorSink } from "./main-error-capture";

type Handler = (event: unknown, ...args: unknown[]) => Promise<unknown>;

// ipcMain stand-in: records what was registered and lets a test invoke it.
function fakeIpc() {
	const handlers = new Map<string, Handler>();
	return {
		ipcMain: { handle: (channel: string, handler: Handler) => void handlers.set(channel, handler) },
		invoke: (channel: string, ...args: unknown[]) => {
			const handler = handlers.get(channel);
			if (!handler) throw new Error(`no handler for ${channel}`);
			return handler({}, ...args);
		},
		channels: () => [...handlers.keys()].sort(),
	};
}

const TEST_ACCOUNT = "test-account";
const signedIn = async () => {};

function registerRemotes(
	ipcMain: Parameters<typeof registerRemotesIpc>[0],
	deps: Omit<RemotesIpcDeps, "getAccountId" | "localIdentity" | "cloudRequest"> & Partial<Pick<RemotesIpcDeps, "getAccountId" | "localIdentity" | "cloudRequest">>,
): void {
	registerRemotesIpc(ipcMain, {
		localIdentity: async () => "h_this_mac",
		cloudRequest: async () => ({ status: 200, body: '{"hosts":[]}' }),
		pair: async () => ({ kind: "unsupported" }),
		authErrorCode: async () => undefined,
		deviceName: async () => "Test Mac",
		...deps,
		getAccountId: deps.getAccountId ?? (async () => TEST_ACCOUNT),
	});
}

// AO Cloud's host directory, as the main process reads it.
function cloudListing(...hosts: Array<{ hostId: string; label: string; url: string }>): RemotesIpcDeps["cloudRequest"] {
	return async () => ({ status: 200, body: JSON.stringify({ hosts: hosts.map((host) => ({ ...host, token: "a".repeat(64) })) }) });
}

async function tempFile(): Promise<string> {
	const dir = await mkdtemp(join(tmpdir(), "ao-remotes-main-"));
	const path = join(dir, "remotes.json");
	await writeFile(path, `{"remotes":[{"hostId":"h_workbox","label":"workbox","url":"http://192.0.2.1:1","password":"old","accountUserId":"${TEST_ACCOUNT}"}]}`, "utf8");
	await chmod(path, 0o600);
	return path;
}

describe("remotesFilePath", () => {
	it("keeps an isolated AO_DATA_DIR out of the real home credentials", () => {
		vi.stubEnv("AO_DATA_DIR", "/tmp/ao-remote-proof");
		try {
			expect(remotesFilePath()).toBe("/tmp/ao-remote-proof/remotes.json");
		} finally {
			vi.unstubAllEnvs();
		}
	});
});

describe("computerName", () => {
	it("prefers the macOS Computer Name and falls back to the host name", async () => {
		await expect(computerName(async () => ({ stdout: "Dhruv’s Mac mini\n" }), "darwin", () => "Dhruvs-Mac-mini.local")).resolves.toBe("Dhruv’s Mac mini");
		await expect(computerName(async () => { throw new Error("scutil missing"); }, "darwin", () => "Dhruvs-Mac-mini.local")).resolves.toBe("Dhruvs-Mac-mini");
		await expect(computerName(async () => ({ stdout: "unused" }), "linux", () => "build-box")).resolves.toBe("build-box");
	});
});

describe("registerRemotesIpc", () => {
	it.each(["offline", "unauthorized", "signed-out", "account-gate", "unexpected"])("classifies %s IPC rejection without changing it", async (state) => {
		resetMainErrorCaptureForTests();
		try {
			const ipc = fakeIpc();
			installIpcErrorCapture(ipc.ipcMain as never);
			const capture = vi.fn();
			setMainErrorSink(capture);
			const unexpected = new TypeError("unexpected probe failure");
			registerRemotes(ipc.ipcMain, {
				file: await tempFile(),
				registry: new RemoteRegistry(async () => { throw new Error("must not connect"); }),
				requireAccount: async () => { if (state === "account-gate") throw new ExpectedIpcError("Sign in to Orchestrator Cloud to use remote hosts."); },
				getAccountId: async () => state === "signed-out" ? "" : TEST_ACCOUNT,
				identity: async () => "h_workbox",
				probe: async () => {
					if (state === "unexpected") throw unexpected;
					return state === "unauthorized" ? "unauthorized" : "offline";
				},
			});
			const result = ipc.invoke("remotes:connect", "http://192.0.2.1:1", "h_workbox");
			if (state === "unexpected") {
				await expect(result).rejects.toBe(unexpected);
				expect(capture).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ category: "ipc", reason: "handler", error: unexpected }));
			} else {
				await expect(result).rejects.toThrow(state === "signed-out" || state === "account-gate" ? "Sign in to Orchestrator Cloud" : `is ${state}`);
				expect(capture).not.toHaveBeenCalled();
			}
		} finally { resetMainErrorCaptureForTests(); }
	});

	it("hides this installation without deleting its account record or original password", async () => {
		const file = await tempFile();
		const original = await readFile(file, "utf8");
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, {
			file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn, localIdentity: async () => "h_workbox",
			cloudRequest: cloudListing({ hostId: "h_workbox", label: "New name", url: "https://rotated.example" }),
		});
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([]);
		expect(await readFile(file, "utf8")).toBe(original);
		await ipc.invoke("remotes:syncAccountHosts", TEST_ACCOUNT);
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([]);
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toEqual([
			{ hostId: "h_workbox", label: "New name", url: "https://rotated.example", password: "old", accountUserId: TEST_ACCOUNT },
		]);
	});

	it.each(["remotes:add", "remotes:update", "remotes:connect", "remotes:linkAccountHost", "remotes:previewUrl", "remotes:resolvePreviewUrl", "remotes:externalUrl"])("rejects direct self access through %s before credentials or proxies", async (channel) => {
		const file = await tempFile();
		const original = await readFile(file, "utf8");
		const probe = vi.fn(async () => "online" as const);
		const start = vi.fn(async () => { throw new Error("proxy must not start"); });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, localIdentity: async () => "h_workbox", identity: async () => "h_workbox", probe });
		const args = channel === "remotes:add" ? [{ label: "self", url: "https://self.example", password: "new" }]
			: channel === "remotes:update" ? ["http://192.0.2.1:1", { password: "new" }]
			: channel.includes("Preview") || channel === "remotes:previewUrl" ? ["h_workbox", "s_1", "http://localhost:3000"]
			: channel === "remotes:externalUrl" ? ["h_workbox", "https://ao-preview-0123456789abcdef0123456789abcdef.localhost:41000/"]
			: ["http://192.0.2.1:1"];
		await expect(ipc.invoke(channel, ...args)).rejects.toThrow(/itself/);
		expect(probe).not.toHaveBeenCalled();
		expect(start).not.toHaveBeenCalled();
		expect(await readFile(file, "utf8")).toBe(original);
	});

	it("refuses an address that resolves to self under another saved host ID before authentication", async () => {
		const file = await tempFile();
		const probe = vi.fn(async () => "online" as const);
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn, identity: async () => "h_this_mac", probe });
		await expect(ipc.invoke("remotes:update", "http://192.0.2.1:1", { url: "https://self.example" })).rejects.toThrow(/itself/);
		await expect(ipc.invoke("remotes:linkAccountHost", "http://192.0.2.1:1")).rejects.toThrow(/itself/);
		expect(probe).not.toHaveBeenCalled();
	});

	it("defers new connections while identity is unknown, then recovers without changing the password", async () => {
		const file = await tempFile();
		const identity = vi.fn(async () => "h_workbox");
		const probe = vi.fn(async () => "online" as const);
		let localHostId: string | null = null;
		const start = vi.fn(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId: string, sourceUrl: string) => sourceUrl, resolvePreviewUrl: (_sessionId: string, viewedUrl: string) => viewedUrl, agentBrowserUrl: async () => "", isAgentBrowserUrl: () => false, externalUrl: (url: string) => url, close: async () => {} }));
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, identity, probe, localIdentity: async () => localHostId });
		await expect(ipc.invoke("remotes:list")).resolves.toHaveLength(1);
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).rejects.toThrow(/ is offline$/);
		expect(identity).not.toHaveBeenCalled();
		expect(probe).not.toHaveBeenCalled();
		expect(start).not.toHaveBeenCalled();
		localHostId = "h_this_mac";
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).resolves.toMatchObject({ hostId: "h_workbox" });
		expect(start).toHaveBeenCalledWith(expect.objectContaining({ password: "old" }));
		localHostId = null;
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).resolves.toMatchObject({ hostId: "h_workbox" });
		expect(start).toHaveBeenCalledOnce();
		expect(probe).toHaveBeenCalledOnce();
	});

	it.each(["remotes:add", "remotes:update", "remotes:linkAccountHost"])("defers %s without presenting a credential when local identity is unknown", async (channel) => {
		const file = await tempFile();
		const original = await readFile(file, "utf8");
		const identity = vi.fn(async () => "h_workbox");
		const probe = vi.fn(async () => "online" as const);
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn, localIdentity: async () => null, identity, probe });
		const args = channel === "remotes:add" ? [{ label: "Other", url: "https://other.example", password: "new" }]
			: channel === "remotes:update" ? ["http://192.0.2.1:1", { password: "new" }] : ["http://192.0.2.1:1"];
		await expect(ipc.invoke(channel, ...args)).rejects.toThrow(/ is offline$/);
		expect(identity).not.toHaveBeenCalled();
		expect(probe).not.toHaveBeenCalled();
		expect(await readFile(file, "utf8")).toBe(original);
	});

	it.each(["http://192.0.2.1:3011", "https://other.trycloudflare.com:443"])("preserves another host's pairing through %s", async (url) => {
		const file = await tempFile();
		await writeFile(file, JSON.stringify({ remotes: [{ hostId: "h_workbox", label: "Other", url, password: "original", accountUserId: TEST_ACCOUNT }] }));
		const original = await readFile(file, "utf8");
		const probe = vi.fn(async () => "online" as const);
		const start = vi.fn(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId: string, sourceUrl: string) => sourceUrl, resolvePreviewUrl: (_sessionId: string, viewedUrl: string) => viewedUrl, agentBrowserUrl: async () => "", isAgentBrowserUrl: () => false, externalUrl: (url: string) => url, close: async () => {} }));
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, identity: async () => "h_workbox", probe });
		await expect(ipc.invoke("remotes:connect", url)).resolves.toMatchObject({ hostId: "h_workbox", url });
		expect(probe).toHaveBeenCalledWith(expect.objectContaining({ url, password: "original" }));
		expect(start).toHaveBeenCalledWith(expect.objectContaining({ url, password: "original" }));
		expect(await readFile(file, "utf8")).toBe(original);
	});

	it("retains an existing verified remote if local identity becomes unavailable during its probe", async () => {
		const file = await tempFile();
		const close = vi.fn(async () => {});
		const registry = new RemoteRegistry(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId, sourceUrl) => sourceUrl, resolvePreviewUrl: (_sessionId, viewedUrl) => viewedUrl, agentBrowserUrl: async () => "", isAgentBrowserUrl: () => false, externalUrl: (url: string) => url, close }));
		await registry.connect({ hostId: "h_workbox", label: "workbox", url: "http://192.0.2.1:1", password: "old" });
		let localHostId: string | null = "h_this_mac";
		const probe = vi.fn(async () => { localHostId = null; return "online" as const; });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry, requireAccount: signedIn, identity: async () => "h_workbox", localIdentity: async () => localHostId, probe });
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).resolves.toMatchObject({ hostId: "h_workbox" });
		expect(close).not.toHaveBeenCalled();
	});

	it("invalidates identity on a same-port restart before allowing another remote connection", async () => {
		const file = await tempFile();
		let generation = 1;
		const localProbe = vi.fn().mockResolvedValueOnce("h_this_mac").mockRejectedValueOnce(new Error("starting")).mockResolvedValueOnce("h_workbox");
		const localIdentity = createLocalHostIdentity(() => ({ port: 4011, generation }), localProbe);
		const start = vi.fn(async () => { throw new Error("unused"); });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, localIdentity });
		await expect(ipc.invoke("remotes:list")).resolves.toHaveLength(1);
		generation++;
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).rejects.toThrow(/ is offline$/);
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([]);
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).rejects.toThrow(/itself/);
		expect(start).not.toHaveBeenCalled();
	});

	it.each(["account", "local identity"])("does not authenticate after %s changes while identity is pending", async (changed) => {
		const file = await tempFile();
		let account = TEST_ACCOUNT;
		let localHostId = "h_this_mac";
		const probe = vi.fn(async () => "online" as const);
		const start = vi.fn(async () => { throw new Error("proxy must not start"); });
		let finish!: (hostId: string) => void;
		const identity = vi.fn(() => new Promise<string>((resolve) => { finish = resolve; }));
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, getAccountId: async () => account, localIdentity: async () => localHostId, identity, probe });
		const pending = ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		await vi.waitFor(() => expect(identity).toHaveBeenCalled());
		if (changed === "account") account = "another-account";
		else localHostId = "h_workbox";
		finish("h_workbox");
		await expect(pending).rejects.toThrow(/changed/);
		expect(probe).not.toHaveBeenCalled();
		expect(start).not.toHaveBeenCalled();
	});

	it("lists the current account rather than an account that signed out while local identity was pending", async () => {
		const file = await tempFile();
		let account = TEST_ACCOUNT;
		let finish!: (hostId: string) => void;
		const localIdentity = vi.fn(() => new Promise<string>((resolve) => { finish = resolve; }));
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn, getAccountId: async () => account, localIdentity });
		const pending = ipc.invoke("remotes:list");
		await vi.waitFor(() => expect(localIdentity).toHaveBeenCalled());
		account = "another-account";
		finish("h_this_mac");
		await expect(pending).resolves.toEqual([]);
	});

	it("does not start a proxy after the account changes during the initial local identity request", async () => {
		const file = await tempFile();
		let account = TEST_ACCOUNT;
		let finish!: (hostId: string) => void;
		const localIdentity = vi.fn().mockImplementationOnce(() => new Promise<string>((resolve) => { finish = resolve; })).mockResolvedValue("h_this_mac");
		const probe = vi.fn(async () => "online" as const);
		const start = vi.fn(async () => { throw new Error("proxy must not start"); });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, getAccountId: async () => account, localIdentity, identity: async () => "h_workbox", probe });
		const pending = ipc.invoke("remotes:connect", "http://192.0.2.1:1");
		await vi.waitFor(() => expect(localIdentity).toHaveBeenCalled());
		account = "another-account";
		finish("h_this_mac");
		await expect(pending).rejects.toThrow(/Account changed/);
		expect(probe).not.toHaveBeenCalled();
		expect(start).not.toHaveBeenCalled();
	});

	it("rejects remote-host access before sign-in without reading saved hosts", async () => {
		const file = await tempFile();
		const registry = new RemoteRegistry(async () => { throw new Error("proxy must not start"); });
		const ipc = fakeIpc();
		const requireAccount = vi.fn(async () => { throw new Error("Sign in to Orchestrator Cloud to use remote hosts."); });
		registerRemotes(ipc.ipcMain, { file, registry, requireAccount });
		await expect(ipc.invoke("remotes:list")).rejects.toThrow(/Sign in to Orchestrator Cloud/);
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).rejects.toThrow(/Sign in to Orchestrator Cloud/);
		await expect(ipc.invoke("remotes:previewUrl", "h_workbox", "s_1", "http://localhost:3000")).rejects.toThrow(/Sign in to Orchestrator Cloud/);
		await expect(ipc.invoke("remotes:externalUrl", "h_workbox", "https://example.com/")).rejects.toThrow(/Sign in to Orchestrator Cloud/);
		expect(requireAccount).toHaveBeenCalledTimes(4);
	});

	it("saves the observed host ID and authenticates only after the identity probe", async () => {
		const requests: Array<{ path: string; authorization: string | undefined }> = [];
		const server = createServer((request, response) => {
			requests.push({ path: request.url ?? "", authorization: request.headers.authorization });
			response.setHeader("content-type", "application/json");
			response.end(JSON.stringify(request.url === "/api/v1/identity"
				? { hostId: "h_new", apiVersion: 1 }
				: { status: "ok", service: "agent-orchestrator-daemon", pid: 1234 }));
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		try {
			const address = server.address();
			if (!address || typeof address === "string") throw new Error("missing test port");
			const url = `http://127.0.0.1:${address.port}`;
			const file = await tempFile();
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file,
				requireAccount: signedIn,
				registry: new RemoteRegistry(async () => { throw new Error("unused"); }),
			});
			await expect(ipc.invoke("remotes:add", { label: "mini", url, password: "secret" })).resolves.toBe("online");
			const saved = JSON.parse(await readFile(file, "utf8")).remotes as Array<{ url: string; hostId: string }>;
			expect(saved.find((entry) => entry.url === url)?.hostId).toBe("h_new");
			expect(requests[0]).toEqual({ path: "/api/v1/identity", authorization: undefined });
			expect(requests.filter((request) => request.authorization)).toEqual([
				{ path: "/healthz", authorization: "Bearer secret" },
			]);
		} finally {
			await new Promise<void>((resolve) => server.close(() => resolve()));
		}
	});

	it("refuses a changed host before sending the saved password", async () => {
		let authenticated = false;
		const server = createServer((request, response) => {
			if (request.headers.authorization) authenticated = true;
			response.setHeader("content-type", "application/json");
			response.end(JSON.stringify(request.url === "/api/v1/identity"
				? { hostId: "h_other", apiVersion: 1 }
				: { status: "ok", service: "agent-orchestrator-daemon", pid: 1234 }));
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		try {
			const address = server.address();
			if (!address || typeof address === "string") throw new Error("missing test port");
			const url = `http://127.0.0.1:${address.port}`;
			const file = await tempFile();
			await writeFile(file, JSON.stringify({ remotes: [{ label: "workbox", url, password: "secret", hostId: "h_expected", accountUserId: TEST_ACCOUNT }] }));
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file,
				requireAccount: signedIn,
				registry: new RemoteRegistry(async () => { throw new Error("proxy must not start"); }),
			});
			await expect(ipc.invoke("remotes:connect", url)).rejects.toThrow(/identity|host/i);
			expect(authenticated).toBe(false);
		} finally {
			await new Promise<void>((resolve) => server.close(() => resolve()));
		}
	});

	it("reports an incompatible host before sending or saving its credential", async () => {
		let authenticated = false;
		const server = createServer((request, response) => {
			if (request.headers.authorization) authenticated = true;
			response.setHeader("content-type", "application/json");
			response.end(JSON.stringify({ hostId: "h_new", apiVersion: 2 }));
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		try {
			const address = server.address();
			if (!address || typeof address === "string") throw new Error("missing test port");
			const url = `http://127.0.0.1:${address.port}`;
			const file = await tempFile();
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file,
				requireAccount: signedIn,
				registry: new RemoteRegistry(async () => { throw new Error("proxy must not start"); }),
			});
			await expect(ipc.invoke("remotes:add", { label: "new", url, password: "secret" })).resolves.toBe("incompatible");
			expect(authenticated).toBe(false);
			expect((JSON.parse(await readFile(file, "utf8")).remotes as Array<{ url: string }>).some((host) => host.url === url)).toBe(false);
		} finally {
			await new Promise<void>((resolve) => server.close(() => resolve()));
		}
	});

	it("keeps the real host connected when another saved host points at its URL", async () => {
		const file = await tempFile();
		const url = "http://127.0.0.1:9123";
		await writeFile(file, JSON.stringify({ remotes: [
			{ hostId: "h_a", label: "A", url, password: "a", accountUserId: TEST_ACCOUNT },
			{ hostId: "h_b", label: "B", url, password: "b", accountUserId: TEST_ACCOUNT },
		] }));
		const closed = vi.fn().mockResolvedValue(undefined);
		const registry = new RemoteRegistry(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId, sourceUrl) => sourceUrl, resolvePreviewUrl: (_sessionId, viewedUrl) => viewedUrl, agentBrowserUrl: async () => "", isAgentBrowserUrl: () => false, externalUrl: (url: string) => url, close: closed }));
		await registry.connect({ hostId: "h_b", label: "B", url, password: "b" });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry, requireAccount: signedIn, identity: async () => "h_b", probe: async () => "online" });

		await expect(ipc.invoke("remotes:connect", url, "h_a")).rejects.toThrow(/identity changed/);
		await expect(ipc.invoke("remotes:connect", url, "h_b")).resolves.toMatchObject({ hostId: "h_b" });
		expect(closed).not.toHaveBeenCalled();
	});

	it("reports an unreachable new address as offline without changing the saved host", async () => {
		const file = await tempFile();
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, {
			file,
			requireAccount: signedIn,
			registry: new RemoteRegistry(async () => { throw new Error("proxy must not start"); }),
			identity: async () => { throw new TypeError("fetch failed"); },
		});

		await expect(ipc.invoke("remotes:update", "http://192.0.2.1:1", { url: "https://new.trycloudflare.com" })).resolves.toBe("offline");
		expect((JSON.parse(await readFile(file, "utf8")).remotes as Array<{ url: string }>)[0].url).toBe("http://192.0.2.1:1");
	});

	it("lists hosts without their passwords", async () => {
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file: await tempFile(), registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn });
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([{ hostId: "h_workbox", label: "workbox", url: "http://192.0.2.1:1" }]);
	});

	it("saves a new host only after it answers as a daemon", async () => {
		const ipc = fakeIpc();
		const file = await tempFile();
		const probe = vi.fn().mockResolvedValueOnce("offline" as const).mockResolvedValueOnce("online" as const);
		registerRemotes(ipc.ipcMain, {
			file,
			requireAccount: signedIn,
			registry: new RemoteRegistry(async () => { throw new Error("unused"); }),
			probe,
			identity: async () => "h_mini",
		});
		const mini = { label: "mini", url: "http://192.0.2.9:9", password: "m" };

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("offline");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(1);

		await expect(ipc.invoke("remotes:add", mini)).resolves.toBe("online");
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toHaveLength(2);
		expect(JSON.parse(await readFile(file, "utf8")).remotes[1].hostId).toBe("h_mini");
	});

	it("drops the proxy of a removed host", async () => {
		const ipc = fakeIpc();
		const closed = vi.fn().mockResolvedValue(undefined);
		const registry = new RemoteRegistry(async () => ({ base: "http://127.0.0.1:7654/token", previewUrl: (_sessionId, sourceUrl) => sourceUrl, resolvePreviewUrl: (_sessionId, viewedUrl) => viewedUrl, agentBrowserUrl: async () => "", isAgentBrowserUrl: () => false, externalUrl: (url: string) => url, close: closed }));
		await registry.connect({ hostId: "h_workbox", label: "workbox", url: "http://192.0.2.1:1", password: "old" });
		registerRemotes(ipc.ipcMain, { file: await tempFile(), registry, requireAccount: signedIn });
		await ipc.invoke("remotes:remove", "http://192.0.2.1:1");
		expect(closed).toHaveBeenCalledOnce();
	});

	it("imports only the signed-in account's hosts and prunes removed imports", async () => {
		const file = await tempFile();
		await writeFile(file, JSON.stringify({ remotes: [
			{ hostId: "h_manual", label: "Manual", url: "https://manual.example", password: "password", accountUserId: "user-a" },
			{ hostId: "h_other", label: "Other", url: "https://other.example", password: "password", accountUserId: "user-b" },
		] }));
		let account = "user-a";
		let listing = cloudListing({ hostId: "h_cloud", label: "Cloud", url: "https://cloud.example" });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, {
			file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }),
			requireAccount: signedIn, getAccountId: async () => account,
			cloudRequest: (...args) => listing(...args),
		});
		await ipc.invoke("remotes:syncAccountHosts", "user-a");
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([
			{ hostId: "h_manual", label: "Manual", url: "https://manual.example" },
			{ hostId: "h_cloud", label: "Cloud", url: "https://cloud.example" },
		]);
		listing = cloudListing();
		await ipc.invoke("remotes:syncAccountHosts", "user-a");
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([
			{ hostId: "h_manual", label: "Manual", url: "https://manual.example" },
		]);
		account = "user-b";
		listing = cloudListing({ hostId: "h_leak", label: "Leak", url: "https://leak.example" });
		await expect(ipc.invoke("remotes:syncAccountHosts", "user-a")).rejects.toThrow(/Account changed/);
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([
			{ hostId: "h_other", label: "Other", url: "https://other.example" },
		]);
	});

	it("preserves and shows a legacy pairing when its host is discovered in the account", async () => {
		const file = await tempFile();
		await writeFile(file, JSON.stringify({ remotes: [
			{ hostId: "h_legacy", label: "Old", url: "https://old.example", password: "pairing-password" },
		] }));
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, {
			file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }),
			requireAccount: signedIn, getAccountId: async () => "user-a",
			cloudRequest: cloudListing({ hostId: "h_legacy", label: "Discovered", url: "https://new.example" }),
		});

		await ipc.invoke("remotes:syncAccountHosts", "user-a");
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([
			{ hostId: "h_legacy", label: "Discovered", url: "https://new.example" },
		]);
		const saved = JSON.parse(await readFile(file, "utf8")) as { remotes: Array<{ password: string }> };
		expect(saved.remotes[0]?.password).toBe("pairing-password");
	});

	it("links a paired host to the account without returning its token", async () => {
		const token = "b".repeat(64);
		const server = createServer((req, res) => {
			res.setHeader("Content-Type", "application/json");
			res.end(JSON.stringify(req.url === "/api/v1/remote-host/account-token" && req.headers.authorization === "Bearer old"
				? { hostId: "h_workbox", token, hostKey: "k".repeat(43) } : {}));
		});
		await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
		try {
			const url = `http://127.0.0.1:${(server.address() as { port: number }).port}`;
			const file = await tempFile();
			await writeFile(file, JSON.stringify({ remotes: [{ hostId: "h_workbox", label: "workbox", url, password: "old", accountUserId: TEST_ACCOUNT }] }));
			const requests: Array<{ method: string; path: string; body?: unknown; deadline: boolean }> = [];
			let finishRegistration = () => {};
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn, identity: async () => "h_workbox",
				cloudRequest: async (method, path, options) => {
					requests.push({ method, path, body: options?.body, deadline: options?.signal !== undefined });
					if (method === "PUT") await new Promise<void>((resolve) => { finishRegistration = resolve; });
					return { status: 200, body: method === "GET" ? '{"hosts":[],"hostKeys":true}' : "" };
				},
			});
			const linked = ipc.invoke("remotes:linkAccountHost", url);
			await vi.waitFor(() => expect(requests).toHaveLength(2));
			// AO Cloud may take more than a minute to register; other handlers do not wait for it.
			await expect(ipc.invoke("remotes:list")).resolves.toHaveLength(1);
			finishRegistration();
			await expect(linked).resolves.toBeUndefined();
			expect(requests).toEqual([
				{ method: "GET", path: "/api/cloud/v1/me/hosts", body: undefined, deadline: true },
				{ method: "PUT", path: "/api/cloud/v1/me/hosts/h_workbox", body: { label: "workbox", url, token, hostKey: "k".repeat(43) }, deadline: false },
			]);
		} finally {
			server.close();
		}
	});

	it("lists saved hosts while AO Cloud is slow to list the account's machines", async () => {
		let answer: (response: { status: number; body: string }) => void = () => {};
		const cloudRequest = vi.fn<RemotesIpcDeps["cloudRequest"]>(() => new Promise((resolve) => { answer = resolve; }));
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file: await tempFile(), registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn, cloudRequest });
		const synced = ipc.invoke("remotes:syncAccountHosts", TEST_ACCOUNT);
		await vi.waitFor(() => expect(cloudRequest).toHaveBeenCalledOnce());
		expect(cloudRequest.mock.calls[0][2]?.signal).toBeInstanceOf(AbortSignal);
		await expect(ipc.invoke("remotes:list")).resolves.toHaveLength(1);
		answer({ status: 200, body: JSON.stringify({ hosts: [{ hostId: "h_cloud", label: "Cloud", url: "https://cloud.example", token: "a".repeat(64) }] }) });
		await synced;
		await expect(ipc.invoke("remotes:list")).resolves.toHaveLength(2);
	});

	it.each(["silent", "unreachable"])("gives up on an AO Cloud that is %s without holding other handlers", async (state) => {
		let signal: AbortSignal | undefined;
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, {
			file: await tempFile(), registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn,
			accountDirectoryTimeoutMs: 20,
			cloudRequest: (_method, _path, options) => {
				signal = options?.signal;
				return state === "silent" ? new Promise(() => {}) : Promise.reject(new TypeError("fetch failed"));
			},
		});
		for (const channel of ["remotes:syncAccountHosts", "remotes:linkAccountHost"]) {
			await expect(ipc.invoke(channel, channel.endsWith("Hosts") ? TEST_ACCOUNT : "http://192.0.2.1:1")).rejects.toBeInstanceOf(ExpectedIpcError);
			if (state === "silent") expect(signal?.aborted).toBe(true);
		}
		await expect(ipc.invoke("remotes:list")).resolves.toHaveLength(1);
	});

	it("applies only the newest of two overlapping syncs", async () => {
		const answers: Array<(response: { status: number; body: string }) => void> = [];
		const listing = (label: string) => ({ status: 200, body: JSON.stringify({ hosts: [{ hostId: "h_cloud", label, url: "https://cloud.example", token: "a".repeat(64) }] }) });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, {
			file: await tempFile(), registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn,
			cloudRequest: () => new Promise((resolve) => { answers.push(resolve); }),
		});
		const older = ipc.invoke("remotes:syncAccountHosts", TEST_ACCOUNT);
		const newer = ipc.invoke("remotes:syncAccountHosts", TEST_ACCOUNT);
		await vi.waitFor(() => expect(answers).toHaveLength(2));
		answers[1](listing("Newer"));
		await newer;
		answers[0](listing("Older"));
		await older;
		await expect(ipc.invoke("remotes:list")).resolves.toContainEqual({ hostId: "h_cloud", label: "Newer", url: "https://cloud.example" });
	});

	it.each([
		["a keychain that cannot decrypt them", { encrypt: (text: string) => `sealed:${text}`, decrypt: (): string => { throw new Error("another keychain"); } }],
		["no keychain", null],
	])("syncs over saved passwords it cannot read with %s", async (_name, cipher) => {
		setPasswordCipher(() => cipher);
		try {
			const file = await tempFile();
			await writeFile(file, JSON.stringify({ remotes: [
				{ hostId: "h_paired", label: "Paired", url: "https://paired.example", sealedPassword: "sealed-pairing", accountUserId: TEST_ACCOUNT },
				{ hostId: "h_token", label: "Token", url: "https://token.example", sealedPassword: "sealed-token", accountToken: true, accountUserId: TEST_ACCOUNT },
				{ hostId: "h_gone", label: "Gone", url: "https://gone.example", sealedPassword: "sealed-gone", accountToken: true, accountUserId: TEST_ACCOUNT },
			] }));
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn,
				cloudRequest: cloudListing(
					{ hostId: "h_paired", label: "Renamed", url: "https://moved.example" },
					{ hostId: "h_token", label: "Token", url: "https://token.example" },
				),
			});
			await ipc.invoke("remotes:syncAccountHosts", TEST_ACCOUNT);
			// Neither password is replaced by the account token, and the host the account dropped goes.
			expect(JSON.parse(await readFile(file, "utf8")).remotes).toEqual([
				{ hostId: "h_paired", label: "Renamed", url: "https://moved.example", sealedPassword: "sealed-pairing", accountUserId: TEST_ACCOUNT },
				{ hostId: "h_token", label: "Token", url: "https://token.example", sealedPassword: "sealed-token", accountToken: true, accountUserId: TEST_ACCOUNT },
			]);
		} finally {
			setPasswordCipher(() => null);
		}
	});

	describe("device tokens", () => {
		const token = "aod_" + "c".repeat(64);
		const proxy = () => vi.fn(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId: string, sourceUrl: string) => sourceUrl, resolvePreviewUrl: (_sessionId: string, viewedUrl: string) => viewedUrl, agentBrowserUrl: async () => "", isAgentBrowserUrl: () => false, externalUrl: (url: string) => url, close: async () => {} }));
		const saved = async (file: string) => (JSON.parse(await readFile(file, "utf8")) as { remotes: Array<Record<string, unknown>> }).remotes;
		type Pair = NonNullable<RemotesIpcDeps["pair"]>;

		it.each([
			["a one-time code", "pairing-code" as const, undefined],
			["the pairing password", "password" as const, "typed-secret"],
		])("adds a host with %s and saves this device's own token", async (_name, credential, fallbackPassword) => {
			const file = await tempFile();
			const pair = vi.fn<Pair>(async () => ({ kind: "paired", token, credential }));
			const probe = vi.fn(async () => "online" as const);
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(proxy()), requireAccount: signedIn, identity: async () => "h_new", probe, pair });
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "typed-secret" })).resolves.toBe("online");
			expect(pair).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ url: "https://new.example", hostId: "h_new" }), "typed-secret", { name: "Test Mac", platform: process.platform });
			expect(probe).toHaveBeenCalledWith(expect.objectContaining({ password: token }));
			expect((await saved(file)).find((entry) => entry.hostId === "h_new")).toEqual({
				hostId: "h_new", label: "New", url: "https://new.example", password: token, accountUserId: TEST_ACCOUNT, ...(fallbackPassword ? { fallbackPassword } : {}),
			});
		});

		it("adds a host without device tokens with the typed password, and saves nothing for a refused one", async () => {
			const file = await tempFile();
			const outcomes: Array<Awaited<ReturnType<Pair>>> = [{ kind: "unsupported" }, { kind: "unauthorized" }];
			const probe = vi.fn(async () => "online" as const);
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(proxy()), requireAccount: signedIn, identity: async () => "h_new", probe, pair: async () => outcomes.shift()! });
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "typed-secret" })).resolves.toBe("online");
			expect((await saved(file)).find((entry) => entry.hostId === "h_new")).toMatchObject({ password: "typed-secret" });
			probe.mockClear();
			await expect(ipc.invoke("remotes:add", { label: "Other", url: "https://other.example", password: "wrong" })).resolves.toBe("unauthorized");
			expect(probe).not.toHaveBeenCalled();
			expect(await saved(file)).toHaveLength(2);
		});

		it("swaps a saved pairing password for this device's own token on connect, keeping the password", async () => {
			const file = await tempFile();
			const pair = vi.fn<Pair>(async () => ({ kind: "paired", token, credential: "password" }));
			const start = proxy();
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, identity: async () => "h_workbox", probe: async () => "online", pair });
			await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).resolves.toMatchObject({ hostId: "h_workbox" });
			expect(pair).toHaveBeenCalledExactlyOnceWith(expect.anything(), "old", expect.anything());
			expect(start).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ password: token }));
			expect(await saved(file)).toEqual([{ hostId: "h_workbox", label: "workbox", url: "http://192.0.2.1:1", password: token, fallbackPassword: "old", accountUserId: TEST_ACCOUNT }]);
			await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
			expect(pair).toHaveBeenCalledOnce();
			expect(start).toHaveBeenCalledOnce();
		});

		it("asks a host without device tokens once, and never swaps under a live connection", async () => {
			const file = await tempFile();
			const outcomes: Array<Awaited<ReturnType<Pair>>> = [{ kind: "offline" }, { kind: "unsupported" }];
			const pair = vi.fn<Pair>(async () => outcomes.shift() ?? { kind: "paired", token, credential: "password" });
			const start = proxy();
			const registry = new RemoteRegistry(start);
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, { file, registry, requireAccount: signedIn, identity: async () => "h_workbox", probe: async () => "online", pair });
			await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
			expect(pair).toHaveBeenCalledTimes(1);
			// Connected: the new credential would restart the proxy, so no swap.
			await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
			expect(pair).toHaveBeenCalledTimes(1);
			await registry.disconnect("http://192.0.2.1:1");
			await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
			await registry.disconnect("http://192.0.2.1:1");
			await ipc.invoke("remotes:connect", "http://192.0.2.1:1");
			expect(pair).toHaveBeenCalledTimes(2);
			expect(await saved(file)).toMatchObject([{ password: "old" }]);
		});

		it.each([
			["a host that went back to a release without device tokens", "BAD_PASSWORD", "old"],
			["a host that removed this device", "DEVICE_REVOKED", token],
			["a host whose answer could not be read", undefined, token],
		])("handles a refused token from %s", async (_name, code, expectedPassword) => {
			const file = await tempFile();
			await writeFile(file, JSON.stringify({ remotes: [{ hostId: "h_workbox", label: "workbox", url: "http://192.0.2.1:1", password: token, fallbackPassword: "old", accountUserId: TEST_ACCOUNT }] }));
			const start = proxy();
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file, registry: new RemoteRegistry(start), requireAccount: signedIn, identity: async () => "h_workbox",
				probe: async (entry) => entry.password === "old" ? "online" : "unauthorized",
				authErrorCode: async () => code,
			});
			const connected = ipc.invoke("remotes:connect", "http://192.0.2.1:1");
			if (code !== "BAD_PASSWORD") {
				// Removed on purpose: the kept password must not bring the device back.
				await expect(connected).rejects.toThrow(/is unauthorized$/);
				expect(start).not.toHaveBeenCalled();
				expect(await saved(file)).toMatchObject([{ password: token, fallbackPassword: "old" }]);
			} else {
				await expect(connected).resolves.toMatchObject({ hostId: "h_workbox" });
				expect(start).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ password: "old" }));
				expect(await saved(file)).toEqual([{ hostId: "h_workbox", label: "workbox", url: "http://192.0.2.1:1", password: expectedPassword, accountUserId: TEST_ACCOUNT }]);
			}
		});

		it("reuses an issued token when the probe after pairing failed, and saves only an online host", async () => {
			const file = await tempFile();
			const pair = vi.fn<Pair>(async () => ({ kind: "paired", token, credential: "pairing-code" }));
			const answers: RemoteHealth[] = ["offline", "online", "offline", "online"];
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file, registry: new RemoteRegistry(proxy()), requireAccount: signedIn, identity: async (entry) => entry.url === "https://new.example" ? "h_new" : "h_workbox",
				probe: async () => answers.shift()!, pair,
			});
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "Code1234" })).resolves.toBe("offline");
			expect((await saved(file)).find((entry) => entry.hostId === "h_new")).toBeUndefined();
			// The retry sends the spent code again; the token issued for it is used instead.
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "Code1234" })).resolves.toBe("online");
			expect((await saved(file)).find((entry) => entry.hostId === "h_new")).toMatchObject({ password: token });
			expect(pair).toHaveBeenCalledOnce();
			await expect(ipc.invoke("remotes:update", "http://192.0.2.1:1", { password: "Code5678" })).resolves.toBe("offline");
			expect((await saved(file)).find((entry) => entry.hostId === "h_workbox")).toMatchObject({ password: "old" });
			await expect(ipc.invoke("remotes:update", "http://192.0.2.1:1", { password: "Code5678" })).resolves.toBe("online");
			expect((await saved(file)).find((entry) => entry.hostId === "h_workbox")).toMatchObject({ password: token });
			expect(pair).toHaveBeenCalledTimes(2);
		});

		it("pairs a newly typed code instead of reusing an earlier attempt's token", async () => {
			const file = await tempFile();
			const pair = vi.fn<Pair>(async () => ({ kind: "paired", token, credential: "pairing-code" }));
			const answers: RemoteHealth[] = ["offline", "online"];
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(proxy()), requireAccount: signedIn, identity: async () => "h_new", probe: async () => answers.shift()!, pair });
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "Code1234" })).resolves.toBe("offline");
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "Code5678" })).resolves.toBe("online");
			expect(pair).toHaveBeenLastCalledWith(expect.anything(), "Code5678", expect.anything());
			expect(pair).toHaveBeenCalledTimes(2);
		});

		it("never reuses another account's unsaved token", async () => {
			const file = await tempFile();
			let account = TEST_ACCOUNT;
			const pair = vi.fn<Pair>(async () => ({ kind: "paired", token, credential: "pairing-code" }));
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, {
				file, registry: new RemoteRegistry(proxy()), requireAccount: signedIn, getAccountId: async () => account,
				identity: async () => "h_new", probe: async () => "offline", pair,
			});
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "Code1234" })).resolves.toBe("offline");
			account = "other-account";
			pair.mockResolvedValueOnce({ kind: "unauthorized" });
			await expect(ipc.invoke("remotes:add", { label: "New", url: "https://new.example", password: "wrong" })).resolves.toBe("unauthorized");
			expect(pair).toHaveBeenCalledTimes(2);
			expect((await saved(file)).find((entry) => entry.hostId === "h_new")).toBeUndefined();
		});

		it("swaps a newly typed code when a saved host is edited", async () => {
			const file = await tempFile();
			const pair = vi.fn<Pair>(async () => ({ kind: "paired", token, credential: "pairing-code" }));
			const ipc = fakeIpc();
			registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(proxy()), requireAccount: signedIn, identity: async () => "h_workbox", probe: async () => "online", pair });
			await expect(ipc.invoke("remotes:update", "http://192.0.2.1:1", { password: "Code1234" })).resolves.toBe("online");
			expect(pair).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ hostId: "h_workbox" }), "Code1234", expect.anything());
			expect(await saved(file)).toEqual([{ hostId: "h_workbox", label: "workbox", url: "http://192.0.2.1:1", password: token, accountUserId: TEST_ACCOUNT }]);
			await expect(ipc.invoke("remotes:update", "http://192.0.2.1:1", { label: "renamed" })).resolves.toBe("online");
			expect(pair).toHaveBeenCalledOnce();
		});
	});
});
