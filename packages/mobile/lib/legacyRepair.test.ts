import { beforeEach, describe, expect, it, vi } from "vitest";
import type { Endpoint } from "./endpoints";

const plain = new Map<string, string>();
const secure = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: vi.fn(async (k: string) => plain.get(k) ?? null),
	setItem: vi.fn(async (k: string, v: string) => void plain.set(k, v)),
	removeItem: vi.fn(async (k: string) => void plain.delete(k)),
} }));
vi.mock("expo-secure-store", () => ({
	getItemAsync: vi.fn(async (k: string) => secure.get(k) ?? null),
	setItemAsync: vi.fn(async (k: string, v: string) => void secure.set(k, v)),
	deleteItemAsync: vi.fn(async (k: string) => void secure.delete(k)),
}));
import { findHost, loadHosts, migrateLegacyConfig, saveHost, setActiveHost, activeHost } from "./hosts";
import { pairFromCode } from "./pairFlow";
import { encodePairingCode } from "./pairingCode";
import { raceEndpoints } from "./race";
const lan: Endpoint = { kind: "lan", host: "192.168.1.42", port: 3011, secure: false };
const tunnel: Endpoint = { kind: "tunnel", host: "example.trycloudflare.com", port: 443, secure: true };
const appearance = { symbol: "server" as const };
const code = (endpoints = [lan, tunnel], hostId = "h_verified") => encodePairingCode({ v: 2, hostId, name: "Desktop", platform: "linux", endpoints, token: "new-test-token" });
const dependencies = (winner = lan) => ({
	race: vi.fn((endpoints: Endpoint[], id: string) => raceEndpoints(endpoints, id, async (endpoint) => {
		if (endpoint.host !== winner.host || endpoint.port !== winner.port || endpoint.secure !== winner.secure) throw new Error("offline");
		return { hostId: "h_verified" };
	}, { graceMs: 0 })),
	verify: vi.fn(async () => {}),
	chooseName: vi.fn(async () => ({ name: "New machine", appearance: { symbol: "laptop" as const } })),
	findHost, saveHost, setActiveHost,
});
beforeEach(async () => {
	plain.clear(); secure.clear(); vi.clearAllMocks();
	// Exercise the real legacy migration, then customize its phone-local identity.
	plain.set("ao.serverConfig", JSON.stringify({ host: lan.host, httpPort: "3011", password: "old-test-token" }));
	await migrateLegacyConfig();
	await saveHost({ ...(await findHost(""))!, name: "My workstation", appearance });
	await setActiveHost("");
});
describe("#7346 legacy reconnect with real pairing and host storage", () => {
	it("replaces the selected legacy entry, keeps its name/icon and removes its old key", async () => {
		const d = dependencies();
		const result = await pairFromCode(code(), d, { repairHost: (await findHost(""))! });
		const hosts = await loadHosts();
		expect(hosts).toHaveLength(1);
		expect(result).toMatchObject({ ok: true, kind: "reconnected", host: { id: "h_verified", name: "My workstation", appearance, token: "new-test-token" } });
		expect(result).not.toHaveProperty("instead");
		expect(secure.has("ao.hostToken.")).toBe(false);
		expect(secure.get("ao.hostToken.h_verified")).toBe("new-test-token");
		expect((await activeHost())?.id).toBe("h_verified");
		expect(d.chooseName).not.toHaveBeenCalled();
	});
	it("does not merge a generic scan just because its endpoint matches", async () => {
		const result = await pairFromCode(code(), dependencies());
		expect(result).toMatchObject({ ok: true, kind: "added" });
		expect(await loadHosts()).toHaveLength(2);
		expect(secure.has("ao.hostToken.")).toBe(true);
	});
	it("does not replace when only an advertised, losing endpoint overlaps", async () => {
		const result = await pairFromCode(code(), dependencies(tunnel), { repairHost: (await findHost(""))! });
		expect(result).toMatchObject({ ok: true, kind: "added", instead: { id: "" } });
		expect(await loadHosts()).toHaveLength(2);
	});
	it.each([{ ...lan, port: 3012 }, { ...lan, secure: true }])("does not infer legacy identity from a different port or TLS mode: %j", async (endpoint) => {
		const result = await pairFromCode(code([endpoint]), dependencies(endpoint), { repairHost: (await findHost(""))! });
		expect(result).toMatchObject({ ok: true, kind: "added" });
		expect(await loadHosts()).toHaveLength(2);
	});
	it("does not replace a known different machine even at the same endpoint", async () => {
		await saveHost({ ...(await findHost(""))!, id: "h_other", name: "Other" });
		const result = await pairFromCode(code(), dependencies(), { repairHost: (await findHost("h_other"))! });
		expect(result).toMatchObject({ ok: true, kind: "added", instead: { id: "h_other" } });
		expect(await findHost("h_other")).not.toBeNull();
	});
	it("leaves legacy metadata and credential untouched after failed verification", async () => {
		const d = dependencies(); d.verify.mockRejectedValueOnce(new Error("401"));
		expect(await pairFromCode(code(), d, { repairHost: (await findHost(""))! })).toMatchObject({ ok: false, reason: "verify-failed" });
		expect((await loadHosts()).map(h => h.id)).toEqual([""]);
		expect(secure.get("ao.hostToken.")).toBe("old-test-token");
	});
	it("rejects mismatched probe identity without replacing the selected legacy entry", async () => {
		const d = dependencies();
		d.race = vi.fn((endpoints: Endpoint[], id: string) => raceEndpoints(endpoints, id, async () => ({ hostId: "h_intruder" })));
		expect(await pairFromCode(code(), d, { repairHost: (await findHost(""))! })).toMatchObject({ ok: false, reason: "none-reachable" });
		expect((await loadHosts()).map(h => h.id)).toEqual([""]);
		expect(d.verify).not.toHaveBeenCalled();
	});
	it("collapses an already-saved scanned identity while preserving the selected local name", async () => {
		await saveHost({ ...(await findHost(""))!, id: "h_verified", name: "Duplicate", token: "older-test-token" });
		expect(await pairFromCode(code(), dependencies(), { repairHost: (await findHost(""))! })).toMatchObject({ ok: true, kind: "reconnected" });
		expect((await loadHosts()).map(h => [h.id, h.name])).toEqual([["h_verified", "My workstation"]]);
	});
});

describe("legacy identity proof independent of the winning endpoint", () => {
	const tailscale: Endpoint = { kind: "tailscale", host: "workstation.example.ts.net", port: 443, secure: true };
	async function secureLegacy() {
		plain.clear(); secure.clear();
		plain.set("ao.serverConfig", JSON.stringify({ host: tailscale.host, httpPort: String(tailscale.port), secure: true, password: "old-test-token" }));
		await migrateLegacyConfig();
		await saveHost({ ...(await findHost(""))!, name: "My workstation", appearance });
		await setActiveHost("");
		return (await findHost(""))!;
	}
	it("repairs a reachable secure legacy pairing when LAN wins the main race", async () => {
		const repair = await secureLegacy();
		const d = dependencies();
		d.race = vi.fn((endpoints: Endpoint[], id: string) => raceEndpoints(endpoints, id, async () => ({ hostId: "h_verified" }), { graceMs: 0 }));
		const result = await pairFromCode(code([lan, tailscale]), d, { repairHost: repair });
		expect(result).toMatchObject({ ok: true, kind: "reconnected", config: { host: lan.host }, host: { id: "h_verified", name: "My workstation", appearance } });
		expect(await loadHosts()).toHaveLength(1);
		expect(d.race).toHaveBeenNthCalledWith(2, [tailscale], "h_verified");
		expect(d.verify).toHaveBeenCalledOnce();
		expect(d.verify).toHaveBeenCalledWith(expect.objectContaining({ host: lan.host, password: "new-test-token" }));
		expect(d.chooseName).not.toHaveBeenCalled();
		expect(secure.has("ao.hostToken.")).toBe(false);
		expect((await activeHost())?.id).toBe("h_verified");
	});
	it.each(["unreachable", "different-id"])("does not replace the legacy pairing when its saved endpoint is %s", async (failure) => {
		const repair = await secureLegacy();
		const d = dependencies();
		d.race = vi.fn((endpoints: Endpoint[], id: string) => raceEndpoints(endpoints, id, async (endpoint) => {
			if (endpoint.host === tailscale.host) {
				if (failure === "unreachable") throw new Error("offline");
				return { hostId: "h_other" };
			}
			return { hostId: "h_verified" };
		}, { graceMs: 0 }));
		const result = await pairFromCode(code([lan, tailscale]), d, { repairHost: repair });
		expect(result).toMatchObject({ ok: true, kind: "added", instead: { id: "" } });
		expect(await loadHosts()).toHaveLength(2);
		expect((await findHost(""))?.token).toBe("old-test-token");
		expect(secure.has("ao.hostToken.")).toBe(true);
	});
	it("does not probe the legacy endpoint after scanned credential verification fails", async () => {
		const repair = await secureLegacy();
		const d = dependencies();
		d.verify.mockRejectedValueOnce(new Error("401"));
		expect(await pairFromCode(code([lan, tailscale]), d, { repairHost: repair })).toMatchObject({ ok: false, reason: "verify-failed" });
		expect(d.race).toHaveBeenCalledOnce();
		expect((await loadHosts()).map(h => h.id)).toEqual([""]);
	});
});

describe("legacy replacement storage failures", () => {
	it("keeps the legacy entry and credential if the new keystore write fails", async () => {
		const store = await import("expo-secure-store");
		vi.mocked(store.setItemAsync).mockRejectedValueOnce(new Error("keystore unavailable"));
		await expect(pairFromCode(code(), dependencies(), { repairHost: (await findHost(""))! })).rejects.toThrow("keystore unavailable");
		expect((await loadHosts()).map(h => h.id)).toEqual([""]);
		expect(secure.get("ao.hostToken.")).toBe("old-test-token");
	});
	it("preserves other machines during replacement", async () => {
		await saveHost({ ...(await findHost(""))!, id: "h_unrelated", name: "Unrelated", token: "unrelated-test-token" });
		await pairFromCode(code(), dependencies(), { repairHost: (await findHost(""))! });
		expect((await loadHosts()).map(h => h.id).sort()).toEqual(["h_unrelated", "h_verified"]);
		expect((await findHost("h_unrelated"))?.token).toBe("unrelated-test-token");
	});
	it("refuses a replacement that still has no daemon identity", async () => {
		await expect(saveHost((await findHost(""))!, { replaceLegacy: true })).rejects.toThrow("verified host id");
		expect((await loadHosts()).map(h => h.id)).toEqual([""]);
	});
});
