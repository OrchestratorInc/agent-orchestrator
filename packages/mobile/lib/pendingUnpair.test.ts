import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Host } from "./hosts";

const plain = new Map<string, string>();
const secure = new Map<string, string>();
vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => plain.get(key) ?? null,
	setItem: async (key: string, value: string) => void plain.set(key, value),
	removeItem: async (key: string) => void plain.delete(key),
} }));
vi.mock("expo-secure-store", () => ({
	getItemAsync: async (key: string) => secure.get(key) ?? null,
	setItemAsync: async (key: string, value: string) => void secure.set(key, value),
	deleteItemAsync: async (key: string) => void secure.delete(key),
}));
vi.mock("./api", async (importOriginal) => ({ ...await importOriginal<typeof import("./api")>(), unpairFromDaemon: vi.fn(async () => {}) }));

import { ApiError, unpairFromDaemon } from "./api";
import { loadHosts, removeHost, saveHost } from "./hosts";
import { flushMachineUnpairs, markMachineUnpaired, MAX_PENDING_UNPAIRS, PENDING_UNPAIR_MAX_AGE_MS, queueMachineUnpair } from "./pendingUnpair";

/** Retries back off per job; move the clock past the first retry delay. */
function advanceClock(ms: number) {
	vi.useFakeTimers({ toFake: ["Date"] });
	vi.setSystemTime(Date.now() + ms);
}

const host: Host = { id: "h_a", name: "A", platform: "darwin", lastConnected: 1,
	token: "secret-a", endpoints: [{ kind: "lan", host: "192.168.1.42", port: 3011, secure: false }] };

beforeEach(() => {
	plain.clear(); secure.clear(); vi.resetAllMocks();
	vi.stubGlobal("fetch", vi.fn(async () => ({ ok: true, json: async () => ({ hostId: "h_a", apiVersion: 1 }) })));
});
afterEach(() => { vi.unstubAllGlobals(); vi.useRealTimers(); });

describe("durable machine unpair cleanup", () => {
	it("persists cleanup without probing or keeping the machine paired", async () => {
		await saveHost(host);
		await queueMachineUnpair(host, "phone-id");
		await removeHost(host.id);
		await markMachineUnpaired(host.id);
		expect(await loadHosts()).toEqual([]);
		expect(fetch).not.toHaveBeenCalled();
		expect([...plain.values()].join("")).not.toContain("secret-a");
		expect([...secure.values()].join("")).toContain("secret-a");
	});

	it("retries an offline removal with the captured credential after the host is gone", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(fetch).mockRejectedValue(new Error("offline"));
		await flushMachineUnpairs();
		expect(unpairFromDaemon).not.toHaveBeenCalled();
		expect(secure.size).toBeGreaterThan(0);
		vi.mocked(fetch).mockResolvedValue({ ok: true, json: async () => ({ hostId: "h_a", apiVersion: 1 }) } as Response);
		advanceClock(30_000);
		await flushMachineUnpairs();
		expect(unpairFromDaemon).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ hostId: "h_a", password: "secret-a" }), "phone-id");
		expect(secure.size).toBe(0);
	});

	it("keeps a failed daemon DELETE for a later retry", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(unpairFromDaemon).mockRejectedValueOnce(new Error("timeout"));
		await flushMachineUnpairs();
		expect(secure.size).toBeGreaterThan(0);
		advanceClock(30_000);
		await flushMachineUnpairs();
		expect(unpairFromDaemon).toHaveBeenCalledTimes(2);
		expect(secure.size).toBe(0);
	});

	it("never sends a forgotten machine's credential to a replacement at its address", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(fetch).mockResolvedValue({ ok: true, json: async () => ({ hostId: "h_other", apiVersion: 1 }) } as Response);
		await flushMachineUnpairs();
		expect(unpairFromDaemon).not.toHaveBeenCalled();
		expect(secure.size).toBeGreaterThan(0);
	});

	it("does not unpair while local removal is still being persisted", async () => {
		await saveHost(host);
		await queueMachineUnpair(host, "phone-id");
		await flushMachineUnpairs();
		expect(unpairFromDaemon).not.toHaveBeenCalled();
		expect(await loadHosts()).toHaveLength(1);
	});

	it("discards old cleanup when the user deliberately pairs the machine again", async () => {
		await queueMachineUnpair(host, "phone-id");
		await markMachineUnpaired(host.id);
		await saveHost({ ...host, token: "new-secret", lastConnected: 2 });
		await flushMachineUnpairs();
		expect(unpairFromDaemon).not.toHaveBeenCalled();
		expect([...secure.values()].join("")).not.toContain("secret-a");
	});

	it("does not delete a deliberate re-pair that finishes during an identity probe", async () => {
		await queueMachineUnpair(host, "phone-id");
		await markMachineUnpaired(host.id);
		let started!: () => void;
		let release!: (response: Response) => void;
		const requestStarted = new Promise<void>((resolve) => { started = resolve; });
		vi.mocked(fetch).mockImplementationOnce(() => { started(); return new Promise((resolve) => { release = resolve; }); });
		const replay = flushMachineUnpairs();
		await requestStarted;
		await saveHost({ ...host, token: "new-secret", lastConnected: 2 });
		release({ ok: true, json: async () => ({ hostId: "h_a", apiVersion: 1 }) } as Response);
		await replay;
		expect(unpairFromDaemon).not.toHaveBeenCalled();
		expect([...secure.values()].join("")).not.toContain("secret-a");
	});

	it("does not let an offline tunnel delay other machines' cleanup", async () => {
		vi.useFakeTimers();
		await queueMachineUnpair({ ...host, endpoints: [{ kind: "tunnel", host: "dead.example", port: 443, secure: true }] }, "phone-id");
		await queueMachineUnpair({ ...host, id: "h_b", token: "secret-b" }, "phone-id");
		vi.mocked(fetch).mockImplementation(async (url) => {
			if (String(url).includes("dead.example")) throw new Error("offline");
			return { ok: true, json: async () => ({ hostId: "h_b", apiVersion: 1 }) } as Response;
		});
		const replay = flushMachineUnpairs();
		await vi.advanceTimersByTimeAsync(500);
		await replay;
		expect(unpairFromDaemon).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ hostId: "h_b", password: "secret-b" }), "phone-id");
	});

	it("abandons a hanging tunnel identity probe without the connection retry window", async () => {
		vi.useFakeTimers();
		await queueMachineUnpair({ ...host, endpoints: [{ kind: "tunnel", host: "dead.example", port: 443, secure: true }] }, "phone-id");
		vi.mocked(fetch).mockImplementation((_url, init) => new Promise((_resolve, reject) => {
			init!.signal!.addEventListener("abort", () => reject(new Error("aborted")));
		}));
		const replay = flushMachineUnpairs();
		await vi.advanceTimersByTimeAsync(3_000);
		await replay;
		expect(fetch).toHaveBeenCalledOnce();
		expect(unpairFromDaemon).not.toHaveBeenCalled();
		expect(secure.size).toBeGreaterThan(0);
	});

	it("keeps a consecutive removal queued when an earlier cleanup finishes", async () => {
		await queueMachineUnpair(host, "phone-id");
		let started!: () => void;
		let release!: () => void;
		const requestStarted = new Promise<void>((resolve) => { started = resolve; });
		const blocked = new Promise<void>((resolve) => { release = resolve; });
		vi.mocked(unpairFromDaemon).mockImplementationOnce(async () => { started(); await blocked; });
		const replay = flushMachineUnpairs();
		await requestStarted;
		await queueMachineUnpair({ ...host, id: "h_b", token: "secret-b" }, "phone-id");
		release(); await replay;
		expect([...secure.values()].join("")).toContain("secret-b");
		vi.mocked(fetch).mockResolvedValue({ ok: true, json: async () => ({ hostId: "h_b", apiVersion: 1 }) } as Response);
		await flushMachineUnpairs();
		expect(unpairFromDaemon).toHaveBeenLastCalledWith(expect.objectContaining({ hostId: "h_b", password: "secret-b" }), "phone-id");
		expect(secure.size).toBe(0);
	});

	it("backs off an unreachable machine instead of probing it on every tick", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(fetch).mockRejectedValue(new Error("offline"));
		await flushMachineUnpairs();
		await flushMachineUnpairs();
		expect(fetch).toHaveBeenCalledOnce();
		advanceClock(30_000);
		await flushMachineUnpairs();
		expect(fetch).toHaveBeenCalledTimes(2);
		advanceClock(30_000);
		await flushMachineUnpairs();
		expect(fetch).toHaveBeenCalledTimes(2);
		advanceClock(30_000);
		await flushMachineUnpairs();
		expect(fetch).toHaveBeenCalledTimes(3);
	});

	it("lets a launch or foreground retry skip the backoff", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(fetch).mockRejectedValueOnce(new Error("offline"));
		await flushMachineUnpairs();
		await flushMachineUnpairs({ ignoreBackoff: true });
		expect(unpairFromDaemon).toHaveBeenCalledOnce();
		expect(secure.size).toBe(0);
	});

	it("drops cleanup the verified machine rejects as unauthorized", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(unpairFromDaemon).mockRejectedValueOnce(new ApiError(401, "unauthorized"));
		await flushMachineUnpairs();
		expect(secure.size).toBe(0);
		expect(plain.has("ao.pendingUnpairIds")).toBe(false);
	});

	it("keeps cleanup after a server error", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(unpairFromDaemon).mockRejectedValueOnce(new ApiError(500, "boom"));
		await flushMachineUnpairs();
		expect([...secure.values()].join("")).toContain("secret-a");
	});

	it("abandons cleanup for a machine unreachable past the retention window", async () => {
		await queueMachineUnpair(host, "phone-id");
		vi.mocked(fetch).mockRejectedValue(new Error("offline"));
		advanceClock(PENDING_UNPAIR_MAX_AGE_MS + 1);
		await flushMachineUnpairs();
		expect(fetch).not.toHaveBeenCalled();
		expect(secure.size).toBe(0);
	});

	it("caps queued cleanups by dropping the oldest", async () => {
		for (let index = 0; index <= MAX_PENDING_UNPAIRS; index++) {
			await queueMachineUnpair({ ...host, id: `h_${index}`, token: `secret-${index}` }, "phone-id");
		}
		expect(secure.size).toBe(MAX_PENDING_UNPAIRS);
		expect(JSON.parse(plain.get("ao.pendingUnpairIds")!)).toHaveLength(MAX_PENDING_UNPAIRS);
		expect([...secure.values()].some((value) => value.includes("\"secret-0\""))).toBe(false);
	});

	it("does not let one corrupted entry stall other cleanups", async () => {
		await queueMachineUnpair(host, "phone-id");
		const [key] = JSON.parse(plain.get("ao.pendingUnpairIds")!) as string[];
		secure.set(key, "{not json");
		await queueMachineUnpair({ ...host, id: "h_b", token: "secret-b" }, "phone-id");
		vi.mocked(fetch).mockResolvedValue({ ok: true, json: async () => ({ hostId: "h_b", apiVersion: 1 }) } as Response);
		await flushMachineUnpairs();
		expect(unpairFromDaemon).toHaveBeenCalledExactlyOnceWith(expect.objectContaining({ hostId: "h_b" }), "phone-id");
		expect(secure.size).toBe(0);
		expect(plain.has("ao.pendingUnpairIds")).toBe(false);
	});
});
