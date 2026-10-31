import { describe, expect, it, vi } from "vitest";
import type { ServerConfig } from "./config";
import {
	connectedSpawnHosts,
	hostRouteMatches,
	initialSpawnHostId,
	previewForConfig,
	promotableHostConfig,
	selectSpawnHost,
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

	it("starts a generic spawn on the connected default and preserves an explicit connected route", () => {
		const hosts = [{ id: "host-a", label: "Alpha" }, { id: "host-b", label: "Beta" }];

		expect(initialSpawnHostId({ routeHostId: undefined, currentHostId: "host-b", hosts })).toBe("host-b");
		expect(initialSpawnHostId({ routeHostId: "host-a", currentHostId: "host-b", hosts })).toBe("host-a");
		expect(initialSpawnHostId({ routeHostId: "missing", currentHostId: "host-b", hosts })).toBeNull();
		expect(initialSpawnHostId({ routeHostId: undefined, currentHostId: "missing", hosts })).toBe("host-a");
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
});
