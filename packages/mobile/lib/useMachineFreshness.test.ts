import { describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: () => ({ remove() {} }) } }));
vi.mock("./store", () => ({ useApp: () => { throw new Error("not used"); } }));

import { EMPTY_LINK_TRACKER } from "./linkHealth";
import type { HostSnapshot } from "./otherHosts";
import { STALE_AFTER_MS } from "./screenState";
import { readFleetFreshness } from "./useMachineFreshness";

const NOW = 10_000_000;
const online = { connected: true, internet: true };

function snapshot(id: string, lastSyncAt: number): HostSnapshot {
	return {
		hostId: id, name: id, appearance: { symbol: "laptop" }, endpoints: [], config: null, connection: "open", loading: false,
		error: null, errorStatus: null, projects: [], projectsKnown: true, sessions: [], orchestrators: [], orchestratorId: null,
		stats: {}, notificationsUnread: 0, lastSyncAt, linkTracker: EMPTY_LINK_TRACKER,
	};
}

describe("readFleetFreshness", () => {
	// The selected machine's snapshot re-reads its sync time only when the
	// board's data changes, so on a quiet board it trails the real polls.
	it("reads the selected machine's sync time through the store's getter", () => {
		const stale = NOW - STALE_AFTER_MS * 4;
		const freshness = readFleetFreshness({
			hostStates: [snapshot("mac", stale), snapshot("pc", stale)],
			phoneNetwork: online,
			currentHostId: "mac",
			getLastSyncAt: () => NOW - 1_000,
		}, NOW);
		expect(freshness.get("mac")).toBe("live");
		expect(freshness.get("pc")).toBe("lagging");
	});
});
