import { describe, expect, it } from "vitest";

import type { AgentModelInfo } from "./api";
import type { ServerConfig } from "./config";
import { createAgentModelCache, nextConnectionEpoch, provenSpawnConfig, type ConnectionEpoch } from "./spawnLoads";

const lan: ServerConfig = { host: "192.168.1.20", httpPort: "3011", muxPort: "", password: "pw", hostId: "host-a", endpointKind: "lan" };
const models = (...ids: string[]) => ids.map((id) => ({ id, label: id }) as AgentModelInfo);

describe("nextConnectionEpoch", () => {
	const start: ConnectionEpoch = { last: null, epoch: 0 };

	it("stays put through an app switch that brings the same connection back", () => {
		let state = nextConnectionEpoch(start, lan);
		state = nextConnectionEpoch(state, null); // backgrounded: the connection is dropped on purpose
		state = nextConnectionEpoch(state, { ...lan }); // verified again: a fresh object, same endpoint and credential
		expect(state.epoch).toBe(0);
	});

	it("moves on when an upgrade race replaces the connection with another endpoint", () => {
		const tunnel: ServerConfig = { ...lan, host: "abc.trycloudflare.com", httpPort: "443", secure: true, endpointKind: "tunnel" };
		const state = nextConnectionEpoch(nextConnectionEpoch(start, tunnel), lan);
		expect(state.epoch).toBe(1);
		expect(state.last).toBe(lan);
	});

	it("moves on for a new credential, and for a return on another address after a gap", () => {
		const paired = nextConnectionEpoch(start, lan);
		expect(nextConnectionEpoch(paired, { ...lan, password: "rotated" }).epoch).toBe(1);
		const away = nextConnectionEpoch(paired, null);
		expect(nextConnectionEpoch(away, { ...lan, host: "100.64.0.9", endpointKind: "tailscale" }).epoch).toBe(1);
	});
});

describe("createAgentModelCache", () => {
	it("asks again only for an agent whose list failed", () => {
		const cache = createAgentModelCache();
		expect(cache.missing("m1", ["claude-code", "codex"])).toEqual(["claude-code", "codex"]);
		cache.record("m1", "claude-code", models("opus"));
		// codex failed: nothing recorded for it.
		expect(cache.lists(["claude-code", "codex"])).toEqual({ "claude-code": models("opus"), codex: [] });
		// The next load (a reconnect, a reload) asks for codex alone.
		expect(cache.missing("m1", ["claude-code", "codex"])).toEqual(["codex"]);
		cache.record("m1", "codex", models("gpt-5"));
		expect(cache.missing("m1", ["claude-code", "codex"])).toEqual([]);
		expect(cache.lists(["claude-code", "codex"]).codex).toEqual(models("gpt-5"));
	});

	it("keeps the same lists object until a list changes", () => {
		const cache = createAgentModelCache();
		cache.missing("m1", ["a"]);
		cache.record("m1", "a", models("x"));
		const first = cache.lists(["a"]);
		expect(cache.lists(["a"])).toBe(first);
		cache.record("m1", "a", models("y"));
		expect(cache.lists(["a"])).not.toBe(first);
	});

	it("forgets another scope's lists and ignores a late answer for one", () => {
		const cache = createAgentModelCache();
		cache.missing("machine-a", ["a"]);
		cache.record("machine-a", "a", models("x"));
		expect(cache.missing("machine-b", ["a"])).toEqual(["a"]);
		cache.record("machine-a", "a", models("late"));
		expect(cache.lists(["a"])).toEqual({ a: [] });
	});
});

// New task can now pick a machine before it has answered, so its own requests
// must not go out on a credential no poll has accepted yet: one revoked while
// the phone was away would add New task's failed requests to the poll's and
// arm the machine's lockout before the person can scan a new code.
describe("provenSpawnConfig", () => {
	it("holds New task's requests until the machine answers a poll on that connection", () => {
		const resumed = { ...lan };
		expect(provenSpawnConfig(null, resumed, "closed")).toBeNull();
		expect(provenSpawnConfig(null, resumed, "connecting")).toBeNull();
		expect(provenSpawnConfig(null, resumed, "open")).toBe(resumed);
	});

	it("keeps a connection that answered through a failed poll, but not one that replaced it", () => {
		expect(provenSpawnConfig(lan, lan, "closed")).toBe(lan);
		const upgraded: ServerConfig = { ...lan, host: "abc.trycloudflare.com", httpPort: "443", secure: true, endpointKind: "tunnel" };
		expect(provenSpawnConfig(lan, upgraded, "connecting")).toBeNull();
		expect(provenSpawnConfig(lan, null, "closed")).toBeNull();
	});
});
