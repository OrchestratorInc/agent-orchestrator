import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

const saved = vi.hoisted(() => ({ hosts: [] as Host[], ignored: new Map<string, string>(), account: { id: "user-a", email: "person@example.com" } as { id: string; email: string } | null, unpaired: [] as string[], previous: null as { id: string; email: string } | null, beforeHostRead: undefined as (() => Promise<void>) | undefined }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => saved.ignored.get(key) ?? null,
	setItem: async (key: string, value: string) => { saved.ignored.set(key, value); },
	removeItem: async (key: string) => { saved.ignored.delete(key); },
} }));
vi.mock("./account", () => ({
	getAccountAccessToken: async () => "access-token", loadAccount: async () => saved.account,
	loadPreviousAccount: async () => saved.previous,
	clearPreviousAccount: async (id: string) => { if (saved.previous?.id === id) saved.previous = null; },
}));
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
import type { Endpoint } from "./endpoints";
import { mergeEndpoints } from "./mergeEndpoints";

const account = { id: "user-a", email: "person@example.com" };
const token = "a".repeat(64);
const host = (id: string) => ({ hostId: id, label: id, url: `https://${id}.example.com:443`, token });

beforeEach(() => {
	saved.hosts = [];
	saved.ignored.clear();
	saved.account = account;
	saved.unpaired = [];
	saved.previous = null;
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

it("classifies directory addresses: named tunnels as tunnels, LAN names as LAN", async () => {
	const id = "h_00000000-0000-0000-0000-000000000005";
	const quick = { kind: "tunnel", host: "old.trycloudflare.com", port: 443, secure: true } as const;
	const pair = (url: string) => {
		saved.hosts = [{ id, name: "Studio", platform: "macos", token: "original-password", lastConnected: 0, endpoints: [quick] }];
		vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hosts: [{ ...host(id), url }] }) })));
	};

	// A fixed address goes beside the pairing's tunnel: a listing the machine
	// stopped updating must not take away the one that works.
	pair("https://h-0d483066194a12a5dc17.useao.dev");
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.endpoints).toEqual([{ kind: "tunnel", host: "h-0d483066194a12a5dc17.useao.dev", port: 443, secure: true }, quick]);

	// A LAN name must not displace the pairing's tunnel, the only route off-network.
	pair("http://studio.local:3011");
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.endpoints).toEqual([{ kind: "lan", host: "studio.local", port: 3011, secure: false }, quick]);

	// Nor may a Tailscale address, https or not.
	pair("https://100.72.46.7:443");
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.endpoints).toEqual([{ kind: "tailscale", host: "100.72.46.7", port: 443, secure: true }, quick]);

	// Nor an https address on a private name, an IP literal or a custom domain.
	for (const [url, name] of [["https://desktop.home.arpa:3011", "desktop.home.arpa"], ["https://[fd00::5]:3011", "[fd00::5]"], ["https://desktop:3011", "desktop"], ["https://ao.example.com:3011", "ao.example.com"]]) {
		pair(url);
		await syncAccountHosts(account);
		expect(saved.hosts[0]?.endpoints).toEqual([{ kind: "lan", host: name, port: 3011, secure: true }, quick]);
	}
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

it("keeps the routes a connect added to an account machine across later syncs", async () => {
	// Every connect merges the machine's advertised routes beside the listed
	// address. Resetting them on each 30 s sync changed the saved routes twice
	// a tick, and each change restarted the machine's connection.
	const tunnel = { kind: "tunnel", host: "h-00000000000000000011.useao.dev", port: 443, secure: true } as const;
	const item = { ...host("h_00000000-0000-0000-0000-000000000011"), url: "https://h-00000000000000000011.useao.dev:443" };
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.endpoints).toEqual([tunnel]);
	const advertised: Endpoint[] = [
		{ kind: "lan", host: "192.168.1.20", port: 3011, secure: false },
		{ kind: "tailscale", host: "100.64.0.20", port: 3011, secure: false },
		tunnel,
	];
	const connected = { ...saved.hosts[0]!, endpoints: mergeEndpoints(saved.hosts[0]!.endpoints, advertised) };
	saved.hosts = [connected];
	await syncAccountHosts(account);
	await syncAccountHosts(account);
	expect(saved.hosts).toEqual([connected]);
	expect(saved.hosts[0]).toBe(connected);
});

it("replaces an account machine's routes when its listed address moves", async () => {
	const item = { ...host("h_00000000-0000-0000-0000-000000000012"), url: "https://h-00000000000000000012.useao.dev:443" };
	saved.hosts = [{ id: item.hostId, name: item.label, platform: "darwin", token, lastConnected: 0, accountUserId: account.id, endpoints: [
		{ kind: "lan", host: "192.168.1.20", port: 3011, secure: false },
		{ kind: "tunnel", host: "h-000000000000000000ff.useao.dev", port: 443, secure: true },
	] }];
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.endpoints).toEqual([{ kind: "tunnel", host: "h-00000000000000000012.useao.dev", port: 443, secure: true }]);
});

it("does not replace a phone's chosen machine name with the account directory label", async () => {
	const item = { ...host("h_00000000-0000-0000-0000-000000000010"), label: "Studio Mac" };
	saved.hosts = [{ id: item.hostId, name: "My production Mac", platform: "darwin", token, lastConnected: 0, accountUserId: account.id, accountLabel: "studio", endpoints: [] }];
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.name).toBe("My production Mac");
	expect(saved.hosts[0]?.accountLabel).toBe("Studio Mac");
});

it("picks up a machine renamed on the account without a new sign-in, keeping its connected routes", async () => {
	const tunnel = { kind: "tunnel", host: "h-00000000000000000013.useao.dev", port: 443, secure: true } as const;
	let item = { ...host("h_00000000-0000-0000-0000-000000000013"), label: "mac-mini", url: "https://h-00000000000000000013.useao.dev:443" };
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]).toMatchObject({ name: "mac-mini", accountLabel: "mac-mini" });
	const lan = { kind: "lan", host: "192.168.1.20", port: 3011, secure: false } as const;
	saved.hosts = [{ ...saved.hosts[0]!, endpoints: [lan, tunnel], appearance: { symbol: "mac-mini" }, lastConnected: 42 }];
	// Only the label changes: same token, same listed address.
	item = { ...item, label: "Prateek's Mac mini" };
	await syncAccountHosts(account);
	expect(saved.hosts[0]).toMatchObject({ name: "Prateek's Mac mini", accountLabel: "Prateek's Mac mini", endpoints: [lan, tunnel], appearance: { symbol: "mac-mini" }, lastConnected: 42 });
});

it("keeps a name chosen on the phone when the account renames the machine later", async () => {
	let item = { ...host("h_00000000-0000-0000-0000-000000000014"), label: "MacBook" };
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	saved.hosts = [{ ...saved.hosts[0]!, name: "Travel laptop" }];
	item = { ...item, label: "Prateek's MacBook Air" };
	await syncAccountHosts(account);
	expect(saved.hosts[0]).toMatchObject({ name: "Travel laptop", accountLabel: "Prateek's MacBook Air" });
	// The phone clearing its rename back to the account's name follows the account again.
	saved.hosts = [{ ...saved.hosts[0]!, name: "Prateek's MacBook Air" }];
	item = { ...item, label: "Work MacBook" };
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.name).toBe("Work MacBook");
});

it("adopts the account's label once for a machine saved before labels were tracked", async () => {
	const listed = { kind: "lan" as const, host: "h_00000000-0000-0000-0000-000000000015.example.com", port: 443, secure: true };
	const item = { ...host("h_00000000-0000-0000-0000-000000000015"), label: "Prateek's Mac mini" };
	saved.hosts = [{ id: item.hostId, name: "mac-mini", platform: "darwin", token, lastConnected: 0, accountUserId: account.id, endpoints: [listed] }];
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]).toMatchObject({ name: "Prateek's Mac mini", accountLabel: "Prateek's Mac mini", endpoints: [listed] });
});

it("keeps the saved name when the account lists a machine without a label", async () => {
	const item = { ...host("h_00000000-0000-0000-0000-000000000016"), label: "  " };
	saved.hosts = [{ id: item.hostId, name: "Studio", platform: "darwin", token, lastConnected: 0, accountUserId: account.id, accountLabel: "Studio", endpoints: [] }];
	vi.stubGlobal("fetch", async () => ({ ok: true, json: async () => ({ hosts: [item] }) }));
	await syncAccountHosts(account);
	expect(saved.hosts[0]?.name).toBe("Studio");
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

it("keeps the phone's machines when the same person signs in under a new WorkOS user ID", async () => {
	const kept = "h_00000000-0000-0000-0000-0000000000a1";
	const hidden = "h_00000000-0000-0000-0000-0000000000a2";
	const dropped = "h_00000000-0000-0000-0000-0000000000a3";
	const lan = { kind: "lan" as const, host: "192.168.1.20", port: 3000, secure: false };
	const listed = { kind: "lan" as const, host: `${kept}.example.com`, port: 443, secure: true };
	saved.previous = { id: "legacy-user", email: "Person@Example.com" };
	saved.hosts = [
		{ id: kept, name: "Studio Mac", appearance: { symbol: "desktop" }, platform: "darwin", endpoints: [listed], token, lastConnected: 42, accountUserId: "legacy-user", accountLabel: "studio" },
		{ id: dropped, name: "Old box", platform: "linux", endpoints: [lan], token, lastConnected: 0, accountUserId: "legacy-user" },
	];
	saved.ignored.set("ao.accountHosts.ignored.legacy-user", JSON.stringify([hidden]));
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hosts: [host(kept), host(hidden)] }) })));
	await syncAccountHosts(account);
	// The machine still on the account is retagged in place; only the one the
	// account no longer lists is unpaired.
	expect(saved.unpaired).toEqual([dropped]);
	expect(saved.hosts).toHaveLength(1);
	expect(saved.hosts[0]).toEqual({ id: kept, name: "Studio Mac", appearance: { symbol: "desktop" }, platform: "darwin", endpoints: [listed], token, lastConnected: 42, accountUserId: account.id, accountLabel: kept });
	// The hidden machine stays hidden under the new ID.
	expect(JSON.parse(saved.ignored.get(`ao.accountHosts.ignored.${account.id}`) ?? "[]")).toEqual([hidden]);
	expect(saved.ignored.has("ao.accountHosts.ignored.legacy-user")).toBe(false);
	expect(saved.previous).toBeNull();
});

it("does not adopt another person's machines after a WorkOS environment change", async () => {
	const theirs = "h_00000000-0000-0000-0000-0000000000b1";
	saved.previous = { id: "legacy-user", email: "someone-else@example.com" };
	saved.hosts = [{ id: theirs, name: "Their Mac", platform: "darwin", endpoints: [], token, lastConnected: 0, accountUserId: "legacy-user" }];
	saved.ignored.set("ao.accountHosts.ignored.legacy-user", JSON.stringify(["h_hidden"]));
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hosts: [host(theirs)] }) })));
	await syncAccountHosts(account);
	expect(saved.unpaired).toEqual([theirs]);
	expect(saved.hosts).toEqual([expect.objectContaining({ id: theirs, name: theirs, accountUserId: account.id })]);
	expect(saved.ignored.has(`ao.accountHosts.ignored.${account.id}`)).toBe(false);
	expect(saved.previous).toBeNull();
});
