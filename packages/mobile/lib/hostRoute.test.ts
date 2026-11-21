import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import {
	connectedSpawnHosts,
	heldPreview,
	hostRouteMatches,
	initialSpawnHostId,
	machineState,
	rejectedMachineCopy,
	rejectedMachineLine,
	rejectionRepairsWithQr,
	previewForConfig,
	promotableHostConfig,
	selectSpawnHost,
	spawnHostRequestIsCurrent,
	spawnHostSelectionReady,
	type SpawnHostState,
} from "./hostRoute";

describe("host-qualified mobile routes", () => {
	it("persists a default before claiming its live connection", async () => {
		const hostRoute = await import("./hostRoute") as typeof import("./hostRoute") & {
			commitHostSwitch?: <T>(hostId: string, actions: {
				persist(hostId: string): Promise<void>;
				claim(hostId: string): T;
			}) => Promise<T>;
		};
		expect(hostRoute.commitHostSwitch).toBeTypeOf("function");
		if (!hostRoute.commitHostSwitch) return;

		const order: string[] = [];
		const claimed = await hostRoute.commitHostSwitch("host-b", {
			persist: async (id) => { order.push(`persist:${id}`); },
			claim: (id) => { order.push(`claim:${id}`); return "snapshot-b"; },
		});
		expect(claimed).toBe("snapshot-b");
		expect(order).toEqual(["persist:host-b", "claim:host-b"]);
	});

	it("does not claim ownership when persistence fails", async () => {
		const hostRoute = await import("./hostRoute") as typeof import("./hostRoute") & {
			commitHostSwitch?: <T>(hostId: string, actions: {
				persist(hostId: string): Promise<void>;
				claim(hostId: string): T;
			}) => Promise<T>;
		};
		expect(hostRoute.commitHostSwitch).toBeTypeOf("function");
		if (!hostRoute.commitHostSwitch) return;

		const claim = vi.fn();
		await expect(hostRoute.commitHostSwitch("host-b", {
			persist: async () => { throw new Error("storage unavailable"); },
			claim,
		})).rejects.toThrow("storage unavailable");
		expect(claim).not.toHaveBeenCalled();
	});

	it("promotes an already-open verified host without dropping its connection", () => {
		const config = { hostId: "host-b", host: "beta.local", httpPort: "3011", muxPort: "14801", password: "secret" } satisfies ServerConfig;

		expect(promotableHostConfig("host-b", { hostId: "host-b", name: "Beta", connection: "open", config })).toBe(config);
		expect(promotableHostConfig("host-b", { hostId: "host-b", name: "Beta", connection: "closed", config })).toBeNull();
		expect(promotableHostConfig("host-b", { hostId: "host-b", name: "Beta", connection: "open", config: { ...config, hostId: "host-a" } })).toBeNull();
	});

	it("offers only connected machines as spawn destinations", () => {
		const hosts: SpawnHostState[] = [
			{ hostId: "host-a", name: "Alpha", connection: "open", config: { hostId: "host-a" } as ServerConfig },
			{ hostId: "host-b", name: "Beta", connection: "connecting", config: { hostId: "host-b" } as ServerConfig },
			{ hostId: "host-c", name: "Gamma", connection: "open", config: null },
			{ hostId: "host-d", name: "Delta", connection: "open", config: { hostId: "someone-else" } as ServerConfig },
		];

		expect(connectedSpawnHosts(hosts)).toEqual([{ id: "host-a", label: "Alpha" }]);
	});

	it("starts a generic spawn on the connected default and preserves an explicit connected route", () => {
		const hosts = [{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }];

		expect(initialSpawnHostId({ routeHostId: undefined, currentHostId: "host-b", hosts })).toBe("host-b");
		expect(initialSpawnHostId({ routeHostId: "host-a", currentHostId: "host-b", hosts })).toBe("host-a");
		expect(initialSpawnHostId({ routeHostId: "missing", currentHostId: "host-b", hosts })).toBeNull();
		expect(initialSpawnHostId({ routeHostId: undefined, currentHostId: "missing", hosts })).toBe("host-a");
	});

	// Reopened after the phone closed the app, the page can open before the
	// store has read the pairings: picked then, it lands on no machine (or
	// Cloud) and never moves to the paired one once they load.
	it("waits for the pairings before picking a machine, routed or not", () => {
		const loaded = [{ hostId: "host-a", loading: false }];
		expect(spawnHostSelectionReady({ configResolved: false, hosts: [], connectedMachineCount: 0 })).toBe(false);
		expect(spawnHostSelectionReady({ configResolved: true, hosts: [], connectedMachineCount: 0 })).toBe(true);
		expect(spawnHostSelectionReady({ configResolved: true, hosts: loaded, connectedMachineCount: 1 })).toBe(true);
		// A machine still connecting, with none connected, is waited for before Cloud becomes the default.
		expect(spawnHostSelectionReady({ configResolved: true, hosts: [{ hostId: "host-a", loading: true }], connectedMachineCount: 0 })).toBe(false);
		// Routed: the machine named is waited for until it is known and has connected.
		expect(spawnHostSelectionReady({ routeHostId: "host-a", configResolved: false, hosts: [], connectedMachineCount: 0 })).toBe(false);
		expect(spawnHostSelectionReady({ routeHostId: "host-a", configResolved: false, hosts: [{ hostId: "host-a", loading: true }], connectedMachineCount: 0 })).toBe(false);
		expect(spawnHostSelectionReady({ routeHostId: "host-a", configResolved: false, hosts: loaded, connectedMachineCount: 1 })).toBe(true);
		expect(spawnHostSelectionReady({ routeHostId: "gone", configResolved: true, hosts: loaded, connectedMachineCount: 1 })).toBe(true);
	});

	// Cloud can load after the machines: picked before it does, a Cloud draft
	// lands on a machine (or none) and is never moved back.
	it("waits for Cloud to sign in or fail before picking for a Cloud route or draft", () => {
		const loaded = [{ hostId: "host-a", loading: false }];
		expect(spawnHostSelectionReady({ routeHostId: "orchestrator-cloud", configResolved: true, hosts: loaded, connectedMachineCount: 1 })).toBe(false);
		expect(spawnHostSelectionReady({ routeHostId: "orchestrator-cloud", configResolved: true, hosts: loaded, connectedMachineCount: 1, cloudResolved: false })).toBe(false);
		expect(spawnHostSelectionReady({ routeHostId: "orchestrator-cloud", configResolved: true, hosts: loaded, connectedMachineCount: 1, cloudResolved: true })).toBe(true);
		expect(spawnHostSelectionReady({ routeHostId: "orchestrator-cloud", configResolved: false, hosts: [], connectedMachineCount: 0, cloudResolved: true })).toBe(false);
		// A machine's route does not wait for Cloud.
		expect(spawnHostSelectionReady({ routeHostId: "host-a", configResolved: true, hosts: loaded, connectedMachineCount: 1, cloudResolved: false })).toBe(true);
	});

	it("rejects catalog results requested for a previously selected machine", () => {
		expect(spawnHostRequestIsCurrent(2, 2, "host-a", "host-a")).toBe(true);
		expect(spawnHostRequestIsCurrent(1, 2, "host-a", "host-a")).toBe(false);
		expect(spawnHostRequestIsCurrent(2, 2, "host-a", "host-b")).toBe(false);
	});

	it("changes the spawn destination before persistence finishes", async () => {
		let selected = "host-a";
		let persisted = "";
		let finish!: () => void;
		const persistence = new Promise<void>((resolve) => { finish = resolve; });

		const pending = selectSpawnHost("host-b", {
			select: (id) => { selected = id; },
			persistDefault: async (id) => { persisted = id; await persistence; },
		});

		expect(selected).toBe("host-b");
		expect(persisted).toBe("host-b");
		finish();
		await pending;
	});

	it("does not treat B's same project ID as A's project", () => {
		expect(hostRouteMatches("host-a", "host-b")).toBe(false);
		expect(hostRouteMatches(undefined, "host-b")).toBe(false);
		expect(hostRouteMatches("host-a", "host-a")).toBe(true);
	});

	it("never pairs A's preview URL with B's credential or a changed endpoint", () => {
		const a = { hostId: "host-a", host: "alice", password: "a" } as ServerConfig;
		const b = { hostId: "host-b", host: "bob", password: "b" } as ServerConfig;
		const loaded = { config: a, value: { url: "http://alice/preview", authenticated: true } };
		expect(previewForConfig(loaded, a, "host-a")).toEqual(loaded.value);
		expect(previewForConfig(loaded, a, undefined)).toBeNull();
		expect(previewForConfig(loaded, b, "host-a")).toBeNull();
		expect(previewForConfig(loaded, b, "host-b")).toBeNull();
		expect(previewForConfig(loaded, { ...a, host: "new-address" }, "host-a")).toBeNull();
	});

	it("keeps the page showing through an app switch, with the credential that loaded it", () => {
		const a = { hostId: "host-a", host: "alice", httpPort: "3011", password: "a" } as ServerConfig;
		const loaded = { config: a, value: { url: "http://alice:3011/preview", authenticated: true } };
		// The connection is away: the page stays, still paired with A's own connection.
		expect(heldPreview(loaded, null, "host-a")).toEqual({ value: loaded.value, config: a });
		// Back on the same endpoint as a new object: the same page, not a reload.
		expect(heldPreview(loaded, { ...a }, "host-a")).toEqual({ value: loaded.value, config: a });
	});

	it("never keeps a preview across a changed endpoint, credential or machine", () => {
		const a = { hostId: "host-a", host: "alice", httpPort: "3011", password: "a" } as ServerConfig;
		const loaded = { config: a, value: { url: "http://alice:3011/preview", authenticated: true } };
		expect(heldPreview(loaded, { ...a, host: "tunnel.example" }, "host-a")).toBeNull();
		expect(heldPreview(loaded, { ...a, password: "rotated" }, "host-a")).toBeNull();
		expect(heldPreview(loaded, null, "host-b")).toBeNull();
		expect(heldPreview(null, a, "host-a")).toBeNull();
	});

	// What an app switch leaves behind: closed on purpose, no failed poll.
	const backgrounded = { unreachable: false, paired: true, connection: "closed", error: null, errorStatus: null };
	const refused = (errorStatus: number) => ({ ...backgrounded, error: "Your machine rejected this phone's password.", errorStatus });

	it("reads a connection an app switch closed as a reconnect, and only a real outage or no pairing as offline", () => {
		expect(machineState(backgrounded)).toBe("ok");
		// A cold start that has not resolved the pairing yet is not offline either.
		expect(machineState({ ...backgrounded, paired: null })).toBe("ok");
		// Reconnecting after the app came back, or connected.
		expect(machineState({ ...backgrounded, connection: "connecting" })).toBe("ok");
		expect(machineState({ ...backgrounded, connection: "open" })).toBe("ok");
		expect(machineState({ ...backgrounded, unreachable: true, error: "Couldn't reach your machine." })).toBe("offline");
		expect(machineState({ ...backgrounded, paired: false })).toBe("offline");
	});

	// The poll stops on these for good, so a screen that waited spun forever.
	it.each([401, 403, 429, 426])("reads a poll the machine refused with %s as rejected", (status) => {
		expect(machineState(refused(status))).toBe("rejected");
		expect(machineState({ ...refused(status), paired: null })).toBe("rejected");
	});

	it("does not read a failure the poll recovers from, or a refusal it is asking past, as rejected", () => {
		// A 5xx answered, and the poll keeps going.
		expect(machineState(refused(500))).toBe("ok");
		// Unanswered: offline only once the link tracker gives up (`unreachable`).
		expect(machineState({ ...backgrounded, error: "Couldn't reach your machine." })).toBe("ok");
		// The poll is asking again (the app came back), or already got through.
		expect(machineState({ ...refused(401), connection: "connecting" })).toBe("ok");
		expect(machineState({ ...refused(401), connection: "open" })).toBe("ok");
		// No pairing at all wins over a stale refusal.
		expect(machineState({ ...refused(401), paired: false })).toBe("offline");
	});

	it("words a refusal as the board does, and offers a new code only where one fixes it", () => {
		expect(rejectedMachineCopy(401)).toMatchObject({ title: "Your machine rejected this phone", icon: "monitor-off" });
		expect(rejectedMachineCopy(403).title).toBe("Your machine rejected this phone");
		expect(rejectedMachineCopy(429).title).toBe("Too many attempts");
		expect(rejectedMachineCopy(426).title).toBe("Orchestrator versions are incompatible");
		expect(rejectedMachineLine(401)).toBe("Your machine rejected this phone. It was removed from that machine, or the password changed. Scan a new code there.");
		expect(rejectionRepairsWithQr(401)).toBe(true);
		expect(rejectionRepairsWithQr(403)).toBe(true);
		// A lockout clears by itself, and a new code does not fix a version mismatch.
		expect(rejectionRepairsWithQr(429)).toBe(false);
		expect(rejectionRepairsWithQr(426)).toBe(false);
	});
});
