import AsyncStorage from "@react-native-async-storage/async-storage";
import { useEffect, useSyncExternalStore } from "react";
import type { MachineSymbol } from "./machineAppearance";
import type { HostSnapshot } from "./otherHosts";

// Development builds only: fake machines added to the fleet the UI reads, so
// the machine chips and Settings' machine list can be checked with a crowd of
// machines without pairing any. Nothing is paired, saved as a machine or sent
// anywhere; a release build never shows the switch or the machines.

const STORAGE_KEY = "ao.dev.emulateMachines";
// Read this way so a test environment without the global still loads the module.
const DEV = typeof __DEV__ !== "undefined" && __DEV__;
const ID_PREFIX = "emulated-";

const FAKES: { name: string; symbol: MachineSymbol; connection: HostSnapshot["connection"] }[] = [
	{ name: "Studio Workstation", symbol: "mac-studio", connection: "closed" },
	{ name: "build-server-01", symbol: "server", connection: "connecting" },
	{ name: "Gaming PC", symbol: "desktop", connection: "closed" },
	{ name: "cloud-vm-eu", symbol: "cloud", connection: "closed" },
	{ name: "Mini Lab", symbol: "mac-mini", connection: "connecting" },
];

// Never connected: a fake that read as open could become the board's live
// machine or a New task target, with no config behind it. Never loading
// either, or the board would wait on it forever.
const EMULATED_SNAPSHOTS: readonly HostSnapshot[] = FAKES.map((fake, index) => ({
	hostId: `${ID_PREFIX}${index + 1}`,
	name: fake.name,
	appearance: { symbol: fake.symbol },
	endpoints: [],
	config: null,
	connection: fake.connection,
	loading: false,
	error: null,
	errorStatus: null,
	projects: [],
	projectsKnown: false,
	sessions: [],
	archive: null,
	orchestrators: [],
	orchestratorId: null,
	stats: {},
	notificationsUnread: 0,
	lastSyncAt: 0,
}));

/** The fake machines' snapshots: the same array every time, so adding them never busts a memo. */
export function emulatedHostSnapshots(): readonly HostSnapshot[] {
	return EMULATED_SNAPSHOTS;
}

export function isEmulatedHost(hostId: string): boolean {
	return hostId.startsWith(ID_PREFIX);
}

let enabled = false;
let loaded = false;
const listeners = new Set<() => void>();

function publish(next: boolean) {
	enabled = next;
	for (const listener of listeners) listener();
}

function subscribe(listener: () => void) {
	listeners.add(listener);
	return () => { listeners.delete(listener); };
}

/** Kept across reloads, so a dev session doesn't have to switch it back on each time. */
export function setEmulatedMachines(next: boolean): void {
	if (!DEV) return;
	publish(next);
	void AsyncStorage.setItem(STORAGE_KEY, next ? "1" : "0").catch(() => {});
}

/** Whether the fake machines are shown. Always false in a release build. */
export function useEmulatedMachines(): boolean {
	const value = useSyncExternalStore(subscribe, () => enabled, () => false);
	useEffect(() => {
		if (!DEV || loaded) return;
		loaded = true;
		void AsyncStorage.getItem(STORAGE_KEY).then((stored) => { if (stored === "1") publish(true); }).catch(() => {});
	}, []);
	return DEV && value;
}
