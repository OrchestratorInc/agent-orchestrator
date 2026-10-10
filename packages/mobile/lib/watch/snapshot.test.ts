import { describe, expect, it } from "vitest";
import type { DashboardSession } from "../api";
import type { HostSnapshot } from "../otherHosts";
import { buildWatchSnapshot } from "./snapshot";

const now = 1_800_000_000_000;
const session = (id: string, over: Partial<DashboardSession> = {}) => ({ id, displayName: `Worker ${id}`, status: "needs_input", mode: "chat", ...over }) as DashboardSession;
const host = (hostId: string, over: Partial<HostSnapshot> = {}) => ({ hostId, name: hostId, connection: "open", config: { hostId, password: "SYNTHETIC_SECRET", hostname: "example.invalid" }, lastSyncAt: now, sessions: [session("same-id")], ...over }) as HostSnapshot;

describe("Watch snapshot", () => {
	it("reuses Needs-you semantics, includes pinned attention, excludes archived and PR-only failures", () => {
		const result = buildWatchSnapshot([host("alpha", { sessions: [session("pinned", { isPinned: true }), session("busy", { status: "working" }), session("ci", { status: "ci_failed" }), session("archived", { isTerminated: true }), session("blocked", { status: "idle", displayStatus: "Blocked" })] })], now);
		expect(result.hosts[0].items.map((item) => item.sessionId)).toEqual(["pinned", "blocked"]);
		expect(result.hosts[0].count).toBe(2);
	});
	it("keeps same session IDs on different hosts separate without sending config or arbitrary fields", () => {
		const result = buildWatchSnapshot([host("alpha"), host("beta")], now);
		expect(result.hosts.map((item) => item.hostId)).toEqual(["alpha", "beta"]);
		expect(JSON.stringify(result)).not.toMatch(/SYNTHETIC_SECRET|example.invalid|password|config|endpoints/);
	});
	it("does not turn an offline or never-loaded host into zero, nor refresh its capture time", () => {
		const previous = buildWatchSnapshot([host("alpha")], now);
		const result = buildWatchSnapshot([host("alpha", { connection: "closed", sessions: [], lastSyncAt: 0 }), host("beta", { connection: "connecting", lastSyncAt: 0 })], now + 60_000, previous);
		expect(result.hosts[0]).toMatchObject({ capturedAt: now, available: false, count: 1 });
		expect(result.hosts[1]).toMatchObject({ capturedAt: null, available: false, count: null, items: [] });
	});
	it("removes forgotten hosts and replaces last-known data only on a successful poll", () => {
		const previous = buildWatchSnapshot([host("alpha"), host("beta")], now);
		const result = buildWatchSnapshot([host("alpha", { sessions: [], lastSyncAt: now + 1000 })], now + 1000, previous);
		expect(result.hosts).toHaveLength(1);
		expect(result.hosts[0]).toMatchObject({ capturedAt: now + 1000, available: true, count: 0, items: [] });
	});
	it("bounds the transfer and reports omitted hosts/items without undercounting", () => {
		const result = buildWatchSnapshot(Array.from({ length: 20 }, (_, i) => host(`host-${i}`, { name: "x".repeat(300), sessions: Array.from({ length: 100 }, (_, j) => session(`session-${j}`, { displayName: "🚀".repeat(400) })) })), now);
		expect(result.omittedHosts).toBe(12);
		expect(result.hosts[0].count).toBe(100);
		expect(result.hosts[0].items).toHaveLength(8);
		expect(Buffer.byteLength(JSON.stringify(result))).toBeLessThan(48_000);
	});
	it("does not trust a mismatched identity, an error, or a future capture timestamp", () => {
		for (const over of [{ config: null }, { error: "unauthorized" }, { lastSyncAt: now + 1000 }]) {
			expect(buildWatchSnapshot([host("alpha", over)], now).hosts[0].available).toBe(false);
		}
	});
});
