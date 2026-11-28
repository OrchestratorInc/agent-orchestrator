import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import {
	autoSpawnHostId,
	connectedSpawnHosts,
	heldPreview,
	hostRouteMatches,
	machineState,
	rejectedMachineCopy,
	rejectedMachineLine,
	rejectionRepairsWithQr,
	previewForConfig,
	promotableHostConfig,
	selectSpawnHost,
	spawnHostNeedsRetry,
	spawnHostRequestIsCurrent,
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

// New task picks its own machine until the person picks one. Back from the
// background, every machine is reconnecting, and a choice made then used to
// stick: no machine at all, another machine, or Cloud, while the board already
// showed the default's workers.
describe("autoSpawnHostId", () => {
	const ready = (hostId: string) => ({ hostId, ready: true, state: "ok" as const });
	const reconnecting = (hostId: string) => ({ hostId, ready: false, state: "ok" as const });
	const offline = (hostId: string) => ({ hostId, ready: false, state: "offline" as const });
	const rejected = (hostId: string) => ({ hostId, ready: false, state: "rejected" as const });
	const loaded = { configResolved: true, cloudResolved: true, cloud: false };

	it("keeps the default machine while it reconnects instead of settling on none", () => {
		expect(autoSpawnHostId({ ...loaded, currentHostId: "host-a", hosts: [reconnecting("host-a")] })).toEqual({ settled: true, hostId: "host-a" });
	});

	it("keeps the default while another machine answers first", () => {
		expect(autoSpawnHostId({ ...loaded, currentHostId: "host-a", hosts: [reconnecting("host-a"), ready("host-b")] })).toEqual({ settled: true, hostId: "host-a" });
	});

	it("does not move a reconnecting default to Cloud", () => {
		expect(autoSpawnHostId({ ...loaded, cloud: true, currentHostId: "host-a", hosts: [reconnecting("host-a")] })).toEqual({ settled: true, hostId: "host-a" });
	});

	it("falls back to a connected machine, then Cloud, only once the default is offline or refused", () => {
		expect(autoSpawnHostId({ ...loaded, cloud: true, currentHostId: "host-a", hosts: [offline("host-a"), ready("host-b")] })).toEqual({ settled: true, hostId: "host-b" });
		expect(autoSpawnHostId({ ...loaded, cloud: true, currentHostId: "host-a", hosts: [rejected("host-a")] })).toEqual({ settled: true, hostId: "orchestrator-cloud" });
		// Nothing else to use: the default stays, so the page says why and picks it up once it is back.
		expect(autoSpawnHostId({ ...loaded, currentHostId: "host-a", hosts: [offline("host-a"), reconnecting("host-b")] })).toEqual({ settled: true, hostId: "host-a" });
	});

	it("keeps a routed machine, reconnecting or offline, and never swaps in another", () => {
		expect(autoSpawnHostId({ ...loaded, routeHostId: "host-a", currentHostId: "host-b", hosts: [reconnecting("host-a"), ready("host-b")] })).toEqual({ settled: true, hostId: "host-a" });
		expect(autoSpawnHostId({ ...loaded, cloud: true, routeHostId: "host-a", currentHostId: "host-b", hosts: [offline("host-a"), ready("host-b")] })).toEqual({ settled: true, hostId: "host-a" });
		expect(autoSpawnHostId({ ...loaded, routeHostId: "gone", currentHostId: "host-b", hosts: [ready("host-b")] })).toEqual({ settled: true, hostId: null });
	});

	it("goes back to a restored draft's machine while it reconnects, and to the default once that one is gone", () => {
		expect(autoSpawnHostId({ ...loaded, draftHostId: "host-b", currentHostId: "host-a", hosts: [ready("host-a"), reconnecting("host-b")] })).toEqual({ settled: true, hostId: "host-b" });
		expect(autoSpawnHostId({ ...loaded, draftHostId: "host-b", currentHostId: "host-a", hosts: [ready("host-a"), offline("host-b")] })).toEqual({ settled: true, hostId: "host-a" });
		expect(autoSpawnHostId({ ...loaded, draftHostId: "forgotten", currentHostId: "host-a", hosts: [reconnecting("host-a")] })).toEqual({ settled: true, hostId: "host-a" });
	});

	it("waits for the pairings, and for Cloud when the route or draft names it", () => {
		expect(autoSpawnHostId({ ...loaded, configResolved: false, currentHostId: undefined, hosts: [] })).toEqual({ settled: false });
		expect(autoSpawnHostId({ ...loaded, configResolved: false, routeHostId: "host-a", hosts: [] })).toEqual({ settled: false });
		expect(autoSpawnHostId({ ...loaded, cloudResolved: false, routeHostId: "orchestrator-cloud", hosts: [ready("host-a")] })).toEqual({ settled: false });
		expect(autoSpawnHostId({ ...loaded, cloud: true, routeHostId: "orchestrator-cloud", hosts: [ready("host-a")] })).toEqual({ settled: true, hostId: "orchestrator-cloud" });
		expect(autoSpawnHostId({ ...loaded, routeHostId: "orchestrator-cloud", hosts: [ready("host-a")] })).toEqual({ settled: true, hostId: null });
		expect(autoSpawnHostId({ ...loaded, cloudResolved: false, draftHostId: "orchestrator-cloud", currentHostId: "host-a", hosts: [ready("host-a")] })).toEqual({ settled: false });
		expect(autoSpawnHostId({ ...loaded, draftHostId: "orchestrator-cloud", currentHostId: "host-a", hosts: [ready("host-a")] })).toEqual({ settled: true, hostId: "host-a" });
	});

	it("settles on no machine only when nothing is paired and Cloud is not signed in", () => {
		expect(autoSpawnHostId({ ...loaded, hosts: [] })).toEqual({ settled: true, hostId: null });
		expect(autoSpawnHostId({ ...loaded, cloud: true, hosts: [] })).toEqual({ settled: true, hostId: "orchestrator-cloud" });
	});
});

describe("spawnHostNeedsRetry", () => {
	it("asks again for a machine whose last try failed and that nothing is retrying yet", () => {
		expect(spawnHostNeedsRetry({ connection: "closed", loading: false, config: null })).toBe(true);
	});

	it("leaves a machine that is connecting, connected, or still holds a verified connection", () => {
		expect(spawnHostNeedsRetry({ connection: "connecting", loading: true, config: null })).toBe(false);
		expect(spawnHostNeedsRetry({ connection: "closed", loading: true, config: null })).toBe(false);
		expect(spawnHostNeedsRetry({ connection: "open", loading: false, config: { hostId: "host-a" } as ServerConfig })).toBe(false);
		// A poll that failed on a verified connection is already polling again.
		expect(spawnHostNeedsRetry({ connection: "closed", loading: false, config: { hostId: "host-a" } as ServerConfig })).toBe(false);
		expect(spawnHostNeedsRetry(undefined)).toBe(false);
	});
});
