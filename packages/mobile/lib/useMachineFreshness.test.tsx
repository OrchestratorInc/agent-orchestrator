import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { HostSnapshot } from "./otherHosts";

const fixtures = vi.hoisted(() => ({ app: {} as Record<string, unknown> }));

vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: () => ({ remove() {} }) } }));
vi.mock("./store", () => ({ useApp: () => fixtures.app }));

import { EMPTY_LINK_TRACKER } from "./linkHealth";
import type { MachineFreshness } from "./machineFreshness";
import { STALE_AFTER_MS } from "./screenState";
import { FRESHNESS_TICK_MS, readFleetFreshness, useMachineFreshness } from "./useMachineFreshness";

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
			primaryHostId: "mac",
			getPrimaryLastSyncAt: () => NOW - 1_000,
		}, NOW);
		expect(freshness.get("mac")).toBe("live");
		expect(freshness.get("pc")).toBe("lagging");
	});

	// A HostScope for another machine repoints currentHostId and getLastSyncAt;
	// the selected machine must still be read through the unscoped pair.
	it("ignores a HostScope's repointed current machine", () => {
		const stale = NOW - STALE_AFTER_MS * 4;
		const scoped = {
			hostStates: [snapshot("mac", stale), snapshot("pc", NOW - 1_000)],
			phoneNetwork: online,
			primaryHostId: "mac",
			getPrimaryLastSyncAt: () => NOW - 1_000,
			currentHostId: "pc",
			getLastSyncAt: () => NOW - 1_000,
		};
		expect(readFleetFreshness(scoped, NOW).get("mac")).toBe("live");
	});
});

describe("useMachineFreshness", () => {
	let renderer: ReactTestRenderer | undefined;
	const seen: ReadonlyMap<string, MachineFreshness>[] = [];
	function Probe() {
		seen.push(useMachineFreshness());
		return null;
	}

	beforeEach(() => {
		vi.useFakeTimers();
		vi.setSystemTime(NOW);
		vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
		seen.length = 0;
		fixtures.app = {
			hostStates: [snapshot("mac", NOW), snapshot("pc", NOW)],
			phoneNetwork: online,
			primaryHostId: "mac",
			getPrimaryLastSyncAt: () => Date.now(),
		};
	});
	afterEach(() => {
		act(() => renderer?.unmount());
		renderer = undefined;
		vi.useRealTimers();
		vi.unstubAllGlobals();
	});

	// The board and its rows must not redraw on the clock: only a change in
	// some machine's state re-renders the caller, with a new map.
	it("re-renders only when some machine's state changes", () => {
		act(() => { renderer = create(<Probe />); });
		expect(seen).toHaveLength(1);
		expect(seen[0].size).toBe(0);

		// Quiet ticks: the selected machine keeps syncing, pc's rows are still recent.
		act(() => { vi.advanceTimersByTime(FRESHNESS_TICK_MS); });
		expect(seen).toHaveLength(1);

		// pc stops syncing; once its rows outlive the threshold the next tick flips it.
		act(() => { vi.advanceTimersByTime(STALE_AFTER_MS); });
		expect(seen).toHaveLength(2);
		expect(seen[1].get("pc")).toBe("lagging");
		expect(seen[1].get("mac")).toBe("live");

		// Still lagging: no further render, and the same map.
		act(() => { vi.advanceTimersByTime(FRESHNESS_TICK_MS * 2); });
		expect(seen).toHaveLength(2);
	});

	it("hands the same map to every render while nothing changes", () => {
		act(() => { renderer = create(<Probe />); });
		act(() => renderer!.update(<Probe />));
		expect(seen).toHaveLength(2);
		expect(seen[1]).toBe(seen[0]);
	});
});
