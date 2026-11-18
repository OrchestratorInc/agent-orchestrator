import { useEffect, useMemo, useReducer, useRef } from "react";
import { AppState } from "react-native";

import { shouldPoll } from "./appStatePoll";
import { UNKNOWN_PHONE_NETWORK } from "./linkHealth";
import { freshnessByHost, freshnessKey, type MachineFreshness } from "./machineFreshness";
import { useApp } from "./store";

type Store = ReturnType<typeof useApp>;

/**
 * How often freshness is checked against the clock. A machine goes from live
 * to lagging within this long of crossing the threshold.
 */
export const FRESHNESS_TICK_MS = 15_000;

const NO_FRESHNESS: ReadonlyMap<string, MachineFreshness> = new Map();

/**
 * Each paired machine's freshness now. The selected machine's sync time is
 * read through the store's getter: its snapshot only re-reads that ref when
 * the board's data changes, so on a quiet board it trails the real polls and
 * would call a healthy machine stale.
 */
export function readFleetFreshness(app: Pick<Store, "hostStates" | "phoneNetwork" | "primaryHostId" | "getPrimaryLastSyncAt">, now: number): Map<string, MachineFreshness> {
	// The unscoped pair: inside a HostScope for another machine, `currentHostId`
	// names that machine and the selected one would read its trailing snapshot.
	const hosts = app.hostStates.map((host) =>
		host.hostId === app.primaryHostId ? { ...host, lastSyncAt: app.getPrimaryLastSyncAt() } : host);
	return freshnessByHost(hosts, app.phoneNetwork ?? UNKNOWN_PHONE_NETWORK, now);
}

/**
 * Each paired machine's freshness, by host id. While every machine is live
 * the map is empty, so a lookup that finds nothing means "live" too.
 *
 * Checks the clock every tick, but re-renders its caller only when some
 * machine's state changes: the board and its rows must not redraw on a timer.
 * The map keeps its identity until then, so it can be passed to memoised rows.
 */
export function useMachineFreshness(): ReadonlyMap<string, MachineFreshness> {
	const app = useApp();
	const appRef = useRef(app);
	appRef.current = app;
	const [, recheck] = useReducer((n: number) => n + 1, 0);
	const current = readFleetFreshness(app, Date.now());
	const key = freshnessKey(current);
	const keyRef = useRef(key);
	keyRef.current = key;

	useEffect(() => {
		let id: ReturnType<typeof setInterval> | undefined;
		const check = () => {
			if (freshnessKey(readFleetFreshness(appRef.current, Date.now())) !== keyRef.current) recheck();
		};
		const start = () => { if (id === undefined) id = setInterval(check, FRESHNESS_TICK_MS); };
		const stop = () => { if (id !== undefined) clearInterval(id); id = undefined; };
		// Stopped in the background, on the same predicate as the poll.
		if (shouldPoll(AppState.currentState)) start();
		const sub = AppState.addEventListener("change", (state) => {
			if (shouldPoll(state)) { check(); start(); } else stop();
		});
		return () => { stop(); sub.remove(); };
	}, []);

	// eslint-disable-next-line react-hooks/exhaustive-deps -- key covers every state in the map
	return useMemo(() => (key ? current : NO_FRESHNESS), [key]);
}
