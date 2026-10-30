import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

const saved = vi.hoisted(() => ({ hosts: [] as Host[], ignored: new Map<string, string>(), account: { id: "user-a", email: "person@example.com" } as { id: string; email: string } | null, unpaired: [] as string[], beforeHostRead: undefined as (() => Promise<void>) | undefined }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => saved.ignored.get(key) ?? null,
	setItem: async (key: string, value: string) => { saved.ignored.set(key, value); },
} }));
vi.mock("./account", () => ({ getAccountAccessToken: async () => "access-token", loadAccount: async () => saved.account }));
vi.mock("./disconnect", () => ({ forgetPairedHost: async (host: Host) => {
	saved.unpaired.push(host.id);
	saved.hosts = saved.hosts.filter((item) => item.id !== host.id);
} }));
vi.mock("./hosts", () => ({
	loadHosts: async () => { await saved.beforeHostRead?.(); return [...saved.hosts]; },
	saveHost: async (host: Host) => { saved.hosts = [host, ...saved.hosts.filter((old) => old.id !== host.id)]; },
	removeHost: async (id: string) => { saved.hosts = saved.hosts.filter((host) => host.id !== id); },
}));

import { cancelAccountHostSync, clearAccountHosts, ignoreAccountHost, syncAccountHosts, unignoreAccountHost } from "./accountHosts";

const account = { id: "user-a", email: "person@example.com" };
const token = "a".repeat(64);
const host = (id: string) => ({ hostId: id, label: id, url: `https://${id}.example.com:443`, token });

beforeEach(() => {
	saved.hosts = [];
	saved.ignored.clear();
	saved.account = account;
	saved.unpaired = [];
	saved.beforeHostRead = undefined;
	vi.unstubAllGlobals();
});

afterEach(() => { vi.unstubAllEnvs(); vi.useRealTimers(); });

it("uses the configured staging directory instead of production", async () => {
	vi.stubEnv("EXPO_PUBLIC_AO_CLOUD_API_BASE_URL", "https://staging-api.aoagents.dev/");
	vi.resetModules();
	const { syncAccountHosts: syncStaging } = await import("./accountHosts");
	const fetch = vi.fn(async () => ({ ok: true, json: async () => ({ hosts: [] }) }));
	vi.stubGlobal("fetch", fetch);
	await syncStaging(account);
	expect(fetch).toHaveBeenCalledWith("https://staging-api.aoagents.dev/api/cloud/v1/me/hosts", expect.any(Object));
});

it("imports every account host and removes only stale account imports", async () => {
	let directory = [host("h_00000000-0000-0000-0000-000000000001"), host("h_00000000-0000-0000-0000-000000000002")];
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hosts: directory }) })));
	await syncAccountHosts(account);
	expect(saved.hosts.map((item) => item.id)).toHaveLength(2);
	expect(saved.hosts.every((item) => item.accountUserId === account.id && item.token === token)).toBe(true);
	saved.hosts.push({ id: "h_manual", name: "Manual", platform: "linux", endpoints: [], token: "password", lastConnected: 0 });
	directory = directory.slice(0, 1);
	await syncAccountHosts(account);
	expect(saved.hosts.map((item) => item.id).sort()).toEqual(["h_00000000-0000-0000-0000-000000000001", "h_manual"]);
});

it("updates a manually paired tunnel without replacing its original password", async () => {
	const id = "h_00000000-0000-0000-0000-000000000003";
	saved.hosts = [{
		id, name: "My VM", platform: "linux", token: "original-password", lastConnected: 0,
		endpoints: [{ kind: "tunnel", host: "old.trycloudflare.com", port: 443, secure: true }],
	}];
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hosts: [{ ...host(id), url: "https://new.trycloudflare.com:443" }] }) })));
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.token).toBe("original-password");
	expect(saved.hosts[0]?.endpoints).toEqual([{ kind: "tunnel", host: "new.trycloudflare.com", port: 443, secure: true }]);
});

it("keeps a forgotten account machine hidden until explicitly re-paired", async () => {
	const item = host("h_00000000-0000-0000-0000-000000000004");
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hosts: [item] }) })));
	await syncAccountHosts(account);
	await ignoreAccountHost(account.id, item.hostId);
	saved.hosts = [];
	await syncAccountHosts(account);
	expect(saved.hosts).toEqual([]);
	await unignoreAccountHost(account.id, item.hostId);
	await syncAccountHosts(account);
	expect(saved.hosts.map((paired) => paired.id)).toEqual([item.hostId]);
});

function deferred<T>() {
	let resolve!: (value: T) => void;
	const promise = new Promise<T>((done) => { resolve = done; });
	return { promise, resolve };
}

it("persists Forget while directory sync is stalled and never imports its late response", async () => {
	const item = host("h_00000000-0000-0000-0000-000000000005");
	const directory = deferred<{ ok: boolean; json(): Promise<{ hosts: typeof item[] }> }>();
	const started = deferred<void>();
	vi.stubGlobal("fetch", async () => { started.resolve(); return directory.promise; });
	const syncing = syncAccountHosts(account);
	await started.promise;
	let ignored = false;
	const forgetting = ignoreAccountHost(account.id, item.hostId).then(() => { ignored = true; });
	for (let tick = 0; tick < 30; tick++) await Promise.resolve();
	const completedBeforeNetwork = ignored;
	directory.resolve({ ok: true, json: async () => ({ hosts: [item] }) });
	await Promise.all([syncing, forgetting]);
	expect(completedBeforeNetwork).toBe(true);
	expect(saved.hosts).toEqual([]);
});

it("keeps both machines ignored when Forget is confirmed consecutively", async () => {
	const items = [host("h_00000000-0000-0000-0000-000000000006"), host("h_00000000-0000-0000-0000-000000000007")];
	await Promise.all(items.map((item) => ignoreAccountHost(account.id, item.hostId)));
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: items }) }));
	await syncAccountHosts(account);
	expect(saved.hosts).toEqual([]);
});

it("retains the chosen host appearance when an account tunnel changes", async () => {
	const item = host("h_00000000-0000-0000-0000-000000000008");
	saved.hosts = [{ id: item.hostId, name: item.label, platform: "darwin", token, lastConnected: 42, accountUserId: account.id,
		appearance: { symbol: "mac-mini" }, endpoints: [{ kind: "tunnel", host: "old.trycloudflare.com", port: 443, secure: true }] }];
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.appearance).toEqual({ symbol: "mac-mini" });
	expect(saved.hosts[0]?.lastConnected).toBe(42);
});

it("does not replace a phone's chosen machine name with the account directory label", async () => {
	const item = host("h_00000000-0000-0000-0000-000000000010");
	saved.hosts = [{ id: item.hostId, name: "My production Mac", platform: "darwin", token, lastConnected: 0, accountUserId: account.id, endpoints: [] }];
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.name).toBe("My production Mac");
});

it("coalesces repeated sync requests while the directory request is pending", async () => {
	const directory = deferred<{ ok: boolean; json(): Promise<{ hosts: [] }> }>();
	const fetch = vi.fn(() => directory.promise);
	vi.stubGlobal("fetch", fetch);
	const first = syncAccountHosts(account);
	const second = syncAccountHosts(account);
	directory.resolve({ ok: true, json: async () => ({ hosts: [] }) });
	await Promise.all([first, second]);
	expect(fetch).toHaveBeenCalledTimes(1);
});

it("clears account hosts with notification cleanup without waiting for an in-flight sync", async () => {
	const item = host("h_00000000-0000-0000-0000-000000000009");
	saved.hosts = [{ id: item.hostId, name: item.label, platform: "darwin", token, lastConnected: 0, accountUserId: account.id, endpoints: [] },
		{ id: "manual", name: "Manual", platform: "linux", token: "password", lastConnected: 0, endpoints: [] }];
	const directory = deferred<{ ok: boolean; json(): Promise<{ hosts: typeof item[] }> }>();
	const started = deferred<void>();
	vi.stubGlobal("fetch", async () => { started.resolve(); return directory.promise; });
	const syncing = syncAccountHosts(account);
	await started.promise;
	saved.account = null;
	let cleared = false;
	const clearing = clearAccountHosts().then(() => { cleared = true; });
	for (let tick = 0; tick < 30; tick++) await Promise.resolve();
	const completedBeforeNetwork = cleared;
	directory.resolve({ ok: true, json: async () => ({ hosts: [item] }) });
	await Promise.all([syncing, clearing]);
	expect(completedBeforeNetwork).toBe(true);
	expect(saved.hosts.map((paired) => paired.id)).toEqual(["manual"]);
	expect(saved.unpaired).toEqual([item.hostId]);
});

it("does not commit a cancelled sync when local host loading finishes late", async () => {
	const item = host("h_00000000-0000-0000-0000-000000000011");
	const reading = deferred<void>();
	const release = deferred<void>();
	let reads = 0;
	saved.beforeHostRead = async () => { if (++reads === 3) { reading.resolve(); await release.promise; } };
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	const syncing = syncAccountHosts(account);
	await reading.promise;
	cancelAccountHostSync();
	release.resolve();
	await syncing;
	expect(saved.hosts).toEqual([]);
});

it("times out a directory response whose headers arrive but body stalls", async () => {
	vi.useFakeTimers();
	const body = deferred<{ hosts: [] }>();
	const reading = deferred<void>();
	vi.stubGlobal("fetch", async (_url: string, init: RequestInit) => ({ ok: true, json: () => {
		reading.resolve();
		return new Promise((resolve, reject) => {
			init.signal!.addEventListener("abort", () => reject(new Error("body aborted")));
			void body.promise.then(resolve);
		});
	} }));
	let timedOut = false;
	const syncing = syncAccountHosts(account).catch((error: Error) => { timedOut = error.message === "body aborted"; });
	await reading.promise;
	await vi.advanceTimersByTimeAsync(10_000);
	const timeoutApplied = timedOut;
	body.resolve({ hosts: [] });
	await syncing;
	expect(timeoutApplied).toBe(true);
});
