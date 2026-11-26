import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const env = vi.hoisted(() => ({ state: "active", listeners: new Set<(state: string) => void>() }));
vi.mock("react-native", () => ({
	AppState: {
		get currentState() { return env.state; },
		addEventListener: (_: string, listener: (state: string) => void) => {
			env.listeners.add(listener);
			return { remove: () => env.listeners.delete(listener) };
		},
	},
}));

import { useReleaseCatalogRereads } from "./useReleaseCatalogRereads";

let renderer: ReactTestRenderer | undefined;
let rereads = -1;
function Probe({ showsRelease }: { showsRelease: boolean }) {
	rereads = useReleaseCatalogRereads(showsRelease);
	return null;
}
const mount = (showsRelease: boolean) => act(() => { renderer = create(<Probe showsRelease={showsRelease} />); });
const update = (showsRelease: boolean) => act(() => renderer!.update(<Probe showsRelease={showsRelease} />));
const wait = (ms: number) => act(() => { vi.advanceTimersByTime(ms); });
const appState = (state: string) => act(() => { env.state = state; env.listeners.forEach((listener) => listener(state)); });

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	env.state = "active";
	env.listeners.clear();
	rereads = -1;
});
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.unstubAllGlobals(); });

it("reads a release list again after 15s, then twice as long each time", () => {
	mount(true);
	expect(rereads).toBe(0);
	wait(14_999);
	expect(rereads).toBe(0);
	wait(1);
	expect(rereads).toBe(1);
	wait(29_999);
	expect(rereads).toBe(1);
	wait(1);
	expect(rereads).toBe(2);
	wait(60_000);
	expect(rereads).toBe(3);
});

it("waits at most five minutes", () => {
	mount(true);
	for (const ms of [15_000, 30_000, 60_000, 120_000, 240_000]) wait(ms);
	expect(rereads).toBe(5);
	wait(299_999);
	expect(rereads).toBe(5);
	wait(1);
	expect(rereads).toBe(6);
});

it("stops once the account's own list is shown", () => {
	mount(true);
	wait(15_000);
	update(false);
	wait(10 * 60_000);
	expect(rereads).toBe(1);
});

it("reads again on return to the foreground and starts the waits over", () => {
	mount(true);
	wait(15_000);
	wait(30_000);
	expect(rereads).toBe(2);
	appState("background");
	appState("active");
	expect(rereads).toBe(3);
	wait(15_000);
	expect(rereads).toBe(4);
});

it("never reads again for the account's own list", () => {
	mount(false);
	appState("background");
	appState("active");
	wait(10 * 60_000);
	expect(rereads).toBe(0);
});
