import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

const saved = vi.hoisted(() => ({ hosts: [] as Host[], ignored: new Map<string, string>() }));
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => saved.ignored.get(key) ?? null,
	setItem: async (key: string, value: string) => { saved.ignored.set(key, value); },
} }));
vi.mock("./account", () => ({ getAccountAccessToken: async () => "access-token", loadAccount: async () => ({ id: "user-a", email: "person@example.com" }) }));
vi.mock("./hosts", () => ({
	loadHosts: async () => [...saved.hosts],
	saveHost: async (host: Host) => { saved.hosts = [host, ...saved.hosts.filter((old) => old.id !== host.id)]; },
	removeHost: async (id: string) => { saved.hosts = saved.hosts.filter((host) => host.id !== id); },
}));

import { ignoreAccountHost, syncAccountHosts, unignoreAccountHost } from "./accountHosts";

const account = { id: "user-a", email: "person@example.com" };
const token = "a".repeat(64);
const host = (id: string) => ({ hostId: id, label: id, url: `https://${id}.example.com:443`, token });

beforeEach(() => {
	saved.hosts = [];
	saved.ignored.clear();
	vi.unstubAllGlobals();
});

afterEach(() => vi.unstubAllEnvs());

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
