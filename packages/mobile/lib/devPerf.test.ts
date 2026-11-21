import { afterEach, beforeEach, expect, it, vi, type MockInstance } from "vitest";

const appState = vi.hoisted(() => ({ listener: undefined as ((state: string) => void) | undefined }));
vi.mock("react-native", () => ({
	AppState: { addEventListener: (_type: string, listener: (state: string) => void) => { appState.listener = listener; return { remove: () => {} }; } },
}));

import { startJsStallProbe } from "./devPerf";

let info: MockInstance<(...args: unknown[]) => void>;

beforeEach(() => {
	vi.useFakeTimers();
	vi.stubGlobal("__DEV__", true);
	vi.stubEnv("EXPO_PUBLIC_AO_JANK", "1");
	delete (globalThis as Record<string, unknown>).__aoJsStallProbe;
	info = vi.spyOn(console, "info").mockImplementation(() => {});
});

afterEach(() => {
	vi.clearAllTimers();
	vi.useRealTimers();
	vi.unstubAllGlobals();
	vi.unstubAllEnvs();
	vi.restoreAllMocks();
	delete (globalThis as Record<string, unknown>).__aoJsStallProbe;
});

/** Holds the JavaScript thread for `ms`: the clock moves, no timer runs. */
function block(ms: number) {
	vi.setSystemTime(Date.now() + ms);
}

// Timers stop while the app is in the background, so the time away is neither
// a stall nor part of the window the blocked share is taken over.
it("leaves time in the background out of the summary's blocked share", () => {
	startJsStallProbe();
	vi.advanceTimersByTime(1_000);
	block(500);
	vi.advanceTimersByTime(50);
	expect(info).toHaveBeenCalledWith(expect.stringMatching(/^\[jank\] JS blocked 5\d\d ms$/));
	appState.listener?.("background");
	vi.setSystemTime(Date.now() + 60_000);
	appState.listener?.("active");
	vi.advanceTimersByTime(9_000);
	const summaries = info.mock.calls.map(([line]) => String(line)).filter((line) => line.includes("stalls,"));
	expect(summaries).toHaveLength(1);
	expect(summaries[0]).toMatch(/^\[jank\] last 10 s: 1 stalls, 5\d\d ms blocked \(5%\)/);
	// Coming back is not itself a stall.
	expect(info.mock.calls.filter(([line]) => String(line).startsWith("[jank] JS blocked"))).toHaveLength(1);
});
