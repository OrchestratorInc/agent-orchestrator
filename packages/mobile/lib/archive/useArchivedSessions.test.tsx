import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

// A page of the archive asked for while its machine's connection is away waits
// for that machine's connection. It must never take another machine's: the
// store's lookup answers "the selected machine" with whichever connection is
// verified now, so a lookup kept from before the selection changed would.
vi.mock("react-native", () => ({ AppState: { currentState: "active", addEventListener: () => ({ remove: () => {} }) } }));
const fixture = vi.hoisted(() => ({
	requests: [] as string[],
	app: null as unknown,
	listeners: new Set<() => void>(),
}));
vi.mock("../api", () => ({
	getArchivedSessions: async (config: { host: string }) => {
		fixture.requests.push(config.host);
		return { sessions: [], nextCursor: null };
	},
}));
vi.mock("../store", async () => {
	const React = await import("react");
	const subscribe = (listener: () => void) => { fixture.listeners.add(listener); return () => { fixture.listeners.delete(listener); }; };
	return { useApp: () => React.useSyncExternalStore(subscribe, () => fixture.app) };
});
import { useArchivedSessions, type ArchiveSource } from "./useArchivedSessions";

type Config = { host: string; hostId: string };
const machineA: Config = { host: "a.lan", hostId: "a" };
const machineB: Config = { host: "b.lan", hostId: "b" };
// As the store builds it: a lookup made for the selected machine, which reads
// the verified connection from a ref (cfgRef) and the other machines' own.
const verified = { current: null as Config | null };
function app(selected: string, primary: Config | null, others: Record<string, Config | null>) {
	verified.current = primary;
	return {
		hostStates: [], phoneNetwork: undefined, getPrimaryLastSyncAt: () => undefined,
		configForHost: (hostId: string) => hostId === selected ? verified.current : others[hostId] ?? null,
	};
}
const setApp = (next: unknown) => act(() => { fixture.app = next; for (const listener of fixture.listeners) listener(); });
const source: ArchiveSource = { hostId: "a", summary: { total: 3, byProject: {} } };
let renderer: ReactTestRenderer | undefined;
function Harness() {
	useArchivedSessions([source], { active: true, exhaustive: false, inline: [], refreshToken: 0 });
	return null;
}
beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	fixture.requests = [];
});
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.unstubAllGlobals(); });

it("waits for its own machine's connection, never the one selected meanwhile", async () => {
	// Machine A is selected, its connection away after an app switch: the first page waits.
	fixture.app = app("a", null, {});
	act(() => { renderer = create(<Harness />); });
	expect(fixture.requests).toEqual([]);
	// Machine B is selected and verified before A comes back.
	setApp(app("b", machineB, { a: null }));
	await act(async () => { await vi.advanceTimersByTimeAsync(1_000); });
	expect(fixture.requests).toEqual([]);
	// A's own connection is verified: the page goes there.
	setApp(app("b", machineB, { a: machineA }));
	await act(async () => { await vi.advanceTimersByTimeAsync(500); });
	expect(fixture.requests).toEqual(["a.lan"]);
});
