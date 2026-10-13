import { describe, expect, it, vi } from "vitest";
import { chmod, mkdtemp, readFile, writeFile } from "node:fs/promises";
import { createServer } from "node:http";
import { tmpdir } from "node:os";
import { join } from "node:path";
import { registerRemotesIpc, remotesFilePath, type RemotesIpcDeps } from "./remotes-main";
import { RemoteRegistry } from "./remote-registry";
import { createLocalHostIdentity } from "./remote-request";

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
	deps: Omit<RemotesIpcDeps, "getAccountId" | "localIdentity"> & Partial<Pick<RemotesIpcDeps, "getAccountId" | "localIdentity">>,
): void {
	registerRemotesIpc(ipcMain, { localIdentity: async () => "h_this_mac", ...deps, getAccountId: deps.getAccountId ?? (async () => TEST_ACCOUNT) });
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

describe("registerRemotesIpc", () => {
	it("hides this installation without deleting its account record or original password", async () => {
		const file = await tempFile();
		const original = await readFile(file, "utf8");
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }), requireAccount: signedIn, localIdentity: async () => "h_workbox" });
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([]);
		expect(await readFile(file, "utf8")).toBe(original);
		await ipc.invoke("remotes:importAccountHost", TEST_ACCOUNT, { hostId: "h_workbox", label: "New name", url: "https://rotated.example", password: "a".repeat(64) });
		await ipc.invoke("remotes:pruneAccountHosts", TEST_ACCOUNT, ["h_workbox"]);
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([]);
		expect(JSON.parse(await readFile(file, "utf8")).remotes).toEqual([
			{ hostId: "h_workbox", label: "New name", url: "https://rotated.example", password: "old", accountUserId: TEST_ACCOUNT },
		]);
	});

	it.each(["remotes:add", "remotes:update", "remotes:connect", "remotes:issueAccountToken", "remotes:previewUrl", "remotes:resolvePreviewUrl"])("rejects direct self access through %s before credentials or proxies", async (channel) => {
		const file = await tempFile();
		const original = await readFile(file, "utf8");
		const probe = vi.fn(async () => "online" as const);
		const start = vi.fn(async () => { throw new Error("proxy must not start"); });
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, { file, registry: new RemoteRegistry(start), requireAccount: signedIn, localIdentity: async () => "h_workbox", identity: async () => "h_workbox", probe });
		const args = channel === "remotes:add" ? [{ label: "self", url: "https://self.example", password: "new" }]
			: channel === "remotes:update" ? ["http://192.0.2.1:1", { password: "new" }]
			: channel.includes("Preview") || channel === "remotes:previewUrl" ? ["h_workbox", "s_1", "http://localhost:3000"]
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
		await expect(ipc.invoke("remotes:issueAccountToken", "http://192.0.2.1:1")).rejects.toThrow(/itself/);
		expect(probe).not.toHaveBeenCalled();
	});

	it("defers new connections while identity is unknown, then recovers without changing the password", async () => {
		const file = await tempFile();
		const identity = vi.fn(async () => "h_workbox");
		const probe = vi.fn(async () => "online" as const);
		let localHostId: string | null = null;
		const start = vi.fn(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId: string, sourceUrl: string) => sourceUrl, resolvePreviewUrl: (_sessionId: string, viewedUrl: string) => viewedUrl, close: async () => {} }));
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

	it.each(["remotes:add", "remotes:update", "remotes:issueAccountToken"])("defers %s without presenting a credential when local identity is unknown", async (channel) => {
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
		const start = vi.fn(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId: string, sourceUrl: string) => sourceUrl, resolvePreviewUrl: (_sessionId: string, viewedUrl: string) => viewedUrl, close: async () => {} }));
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
		const registry = new RemoteRegistry(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId, sourceUrl) => sourceUrl, resolvePreviewUrl: (_sessionId, viewedUrl) => viewedUrl, close }));
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
		const requireAccount = vi.fn(async () => { throw new Error("Sign in to AO Cloud to use remote hosts."); });
		registerRemotes(ipc.ipcMain, { file, registry, requireAccount });
		await expect(ipc.invoke("remotes:list")).rejects.toThrow(/Sign in to AO Cloud/);
		await expect(ipc.invoke("remotes:connect", "http://192.0.2.1:1")).rejects.toThrow(/Sign in to AO Cloud/);
		await expect(ipc.invoke("remotes:previewUrl", "h_workbox", "s_1", "http://localhost:3000")).rejects.toThrow(/Sign in to AO Cloud/);
		expect(requireAccount).toHaveBeenCalledTimes(3);
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
		const registry = new RemoteRegistry(async () => ({ base: "http://127.0.0.1:5000/token", previewUrl: (_sessionId, sourceUrl) => sourceUrl, resolvePreviewUrl: (_sessionId, viewedUrl) => viewedUrl, close: closed }));
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
		const registry = new RemoteRegistry(async () => ({ base: "http://127.0.0.1:7654/token", previewUrl: (_sessionId, sourceUrl) => sourceUrl, resolvePreviewUrl: (_sessionId, viewedUrl) => viewedUrl, close: closed }));
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
		const ipc = fakeIpc();
		registerRemotes(ipc.ipcMain, {
			file, registry: new RemoteRegistry(async () => { throw new Error("unused"); }),
			requireAccount: signedIn, getAccountId: async () => account,
		});
		await ipc.invoke("remotes:importAccountHost", "user-a", { hostId: "h_cloud", label: "Cloud", url: "https://cloud.example", password: "a".repeat(64) });
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([
			{ hostId: "h_manual", label: "Manual", url: "https://manual.example" },
			{ hostId: "h_cloud", label: "Cloud", url: "https://cloud.example" },
		]);
		await ipc.invoke("remotes:pruneAccountHosts", "user-a", []);
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([
			{ hostId: "h_manual", label: "Manual", url: "https://manual.example" },
		]);
		account = "user-b";
		await expect(ipc.invoke("remotes:importAccountHost", "user-a", { hostId: "h_leak", label: "Leak", url: "https://leak.example", password: "a".repeat(64) })).rejects.toThrow(/Invalid account/);
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
		});

		await ipc.invoke("remotes:importAccountHost", "user-a", {
			hostId: "h_legacy", label: "Discovered", url: "https://new.example", password: "a".repeat(64),
		});
		await expect(ipc.invoke("remotes:list")).resolves.toEqual([
			{ hostId: "h_legacy", label: "Discovered", url: "https://new.example" },
		]);
		const saved = JSON.parse(await readFile(file, "utf8")) as { remotes: Array<{ password: string }> };
		expect(saved.remotes[0]?.password).toBe("pairing-password");
	});
});
