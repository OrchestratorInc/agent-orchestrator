import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { SourceStatus } from "./environment/scopedBoard";

const state = vi.hoisted(() => ({ clocks: {} as Record<string, number>, legacy: 0, retry: vi.fn() }));
vi.mock("react-native", () => ({
	AppState: { currentState: "active", addEventListener: () => ({ remove() {} }) },
	Pressable: "Pressable", Text: "Text", View: "View", StyleSheet: { create: (styles: unknown) => styles },
}));
vi.mock("./store", () => ({ useApp: () => ({
	getLastSyncAt: () => state.legacy,
	getSourceLastSyncAt: (key: string) => state.clocks[key] ?? 0,
	config: { host: "active.local" }, environment: "local",
}) }));
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: { tap: vi.fn() } }));
vi.mock("./ThemeProvider", async () => {
	const { darkTheme } = await import("./theme");
	return { useTheme: () => darkTheme, useThemedStyles: (make: (t: typeof darkTheme) => unknown) => make(darkTheme) };
});

import { StaleBanner } from "./StaleBanner";

const local = '["local","mac"]', other = '["local","other"]', cloud = '["cloud","mac"]';
const healthy: SourceStatus = { resolved: true, available: true, loading: false, stale: false, error: null };
let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	vi.useFakeTimers(); vi.setSystemTime(new Date("2026-10-21T10:00:00Z"));
	state.clocks = {}; state.legacy = Date.now(); state.retry.mockClear();
	const original = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => { if (!String(args[0]).startsWith("react-test-renderer is deprecated")) original(...args); });
});
afterEach(async () => {
	await act(async () => renderer?.unmount()); renderer = undefined;
	vi.useRealTimers(); vi.restoreAllMocks(); vi.unstubAllGlobals();
});
const alerts = () => renderer!.root.findAllByProps({ accessibilityRole: "alert" }).map((node) => node.props.accessibilityLabel);

it("ages each scoped source independently without ticking its containing list", async () => {
	state.clocks = { [local]: Date.now(), [other]: Date.now(), [cloud]: Date.now() };
	let listRenders = 0;
	function List() {
		listRenders++;
		return <>{[[local, "Local"], [other, "Local"], [cloud, "Cloud"]].map(([key, label]) =>
			<StaleBanner key={key} sourceKey={key} sourceLabel={label as "Local" | "Cloud"} sourceStatus={healthy} onRetry={state.retry} />)}</>;
	}
	await act(async () => { renderer = create(<List />); });
	expect(alerts()).toEqual([]);
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	state.clocks[other] = Date.now(); state.clocks[cloud] = Date.now(); state.legacy = Date.now();
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	expect(alerts()).toEqual([expect.stringContaining("Local may be out of date")]);
	expect(listRenders).toBe(1);
	state.clocks[local] = Date.now();
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	expect(alerts()).toEqual([expect.stringContaining("Local may be out of date"), expect.stringContaining("Cloud may be out of date")]);
	expect(listRenders).toBe(1);
	state.clocks[other] = Date.now(); state.clocks[cloud] = Date.now();
	await act(async () => { await vi.advanceTimersByTimeAsync(15_000); });
	// Only the first machine is now old, not the just-refreshed peers.
	expect(alerts()).toEqual([expect.stringContaining("Local may be out of date")]);
});

it("does not borrow the active machine's clock for an unsynced scoped source", async () => {
	state.legacy = Date.now() - 120_000;
	await act(async () => { renderer = create(<StaleBanner sourceKey={cloud} sourceLabel="Cloud" sourceStatus={healthy} />); });
	await act(async () => { await vi.advanceTimersByTimeAsync(60_000); });
	expect(alerts()).toEqual([]);
});

it("shows source errors immediately and preserves Retry even before the first sync", async () => {
	await act(async () => { renderer = create(<StaleBanner sourceKey={cloud} sourceLabel="Cloud" sourceStatus={{ ...healthy, error: "Connection failed" }} onRetry={state.retry} />); });
	expect(alerts()).toEqual(["Cloud couldn't refresh. Connection failed"]);
	await act(async () => renderer!.root.findByProps({ accessibilityLabel: "Retry now" }).props.onPress());
	expect(state.retry).toHaveBeenCalledOnce();
});

it("keeps the legacy single-machine age warning", async () => {
	state.legacy = Date.now() - 120_000;
	await act(async () => { renderer = create(<StaleBanner />); });
	expect(alerts()).toEqual(["Showing data from 2m ago"]);
});
