import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";

vi.mock("@react-native-async-storage/async-storage", () => ({ default: {} }));
vi.mock("expo-secure-store", () => ({}));
vi.mock("./api", () => ({ getSessionPR: vi.fn() }));

import { DEFAULT_CONFIG } from "./config";
import { getSessionPR, type SessionPRSummary } from "./api";
import { prSummaryCacheKey, usePRSummaries, type PRSummaryLookup, type PRSummarySession } from "./usePRSummaries";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });

let lookup: PRSummaryLookup;
function Probe({ targets }: { targets: PRSummarySession[] }) {
	lookup = usePRSummaries(targets);
	return null;
}
const mounts: ReactTestRenderer[] = [];
async function mount(targets: PRSummarySession[]) {
	let renderer!: ReactTestRenderer;
	await act(async () => { renderer = create(createElement(Probe, { targets })); });
	mounts.push(renderer);
	return renderer;
}
afterEach(() => {
	act(() => mounts.splice(0).forEach(renderer => renderer.unmount()));
	vi.restoreAllMocks();
	vi.mocked(getSessionPR).mockReset();
});

describe("PR details across tab navigation", () => {
	it("reuses successful details on remount but refresh explicitly fetches again", async () => {
		const config = { ...DEFAULT_CONFIG, hostId: "navigation-cache-host" };
		const targets = [{ config, sessionId: "session-1" }];
		const summary = { number: 42, title: "Loaded title" } as SessionPRSummary;
		vi.mocked(getSessionPR).mockResolvedValue([summary]);
		const first = await mount(targets);
		act(() => first.unmount());
		await mount(targets);
		expect(getSessionPR).toHaveBeenCalledTimes(1);
		expect(lookup.summaryFor(config, "session-1", 42)).toBe(summary);
		await act(async () => lookup.reload());
		expect(getSessionPR).toHaveBeenCalledTimes(2);
	});
	it("expires navigation reuse after a minute", async () => {
		const now = vi.spyOn(Date, "now").mockReturnValue(1_000);
		const config = { ...DEFAULT_CONFIG, hostId: "expiry-cache-host" };
		vi.mocked(getSessionPR).mockResolvedValue([]);
		const first = await mount([{ config, sessionId: "session-1" }]);
		act(() => first.unmount());
		now.mockReturnValue(61_001);
		await mount([{ config, sessionId: "session-1" }]);
		expect(getSessionPR).toHaveBeenCalledTimes(2);
	});
	it("does not share details between machines or after credentials change", async () => {
		const config = { ...DEFAULT_CONFIG, hostId: "credential-cache-host", password: "first" };
		vi.mocked(getSessionPR).mockResolvedValue([]);
		const first = await mount([{ config, sessionId: "same-session" }]);
		act(() => first.unmount());
		await mount([{ config: { ...config, hostId: "different-cache-host" }, sessionId: "same-session" }]);
		await mount([{ config: { ...config, password: "changed" }, sessionId: "same-session" }]);
		expect(getSessionPR).toHaveBeenCalledTimes(3);
	});
	it("retries a failed visit rather than sharing a cached failure", async () => {
		const config = { ...DEFAULT_CONFIG, hostId: "failed-cache-host" };
		vi.mocked(getSessionPR).mockRejectedValueOnce(new Error("offline")).mockResolvedValue([]);
		const first = await mount([{ config, sessionId: "session-1" }]);
		act(() => first.unmount());
		await mount([{ config, sessionId: "session-1" }]);
		expect(getSessionPR).toHaveBeenCalledTimes(2);
	});
	it("re-fetches when credentials change while the screen remains mounted", async () => {
		const config = { ...DEFAULT_CONFIG, hostId: "live-credential-cache-host", password: "first" };
		vi.mocked(getSessionPR).mockResolvedValue([]);
		const screen = await mount([{ config, sessionId: "session-1" }]);
		await act(async () => screen.update(createElement(Probe, { targets: [{ config: { ...config, password: "new" }, sessionId: "session-1" }] })));
		expect(getSessionPR).toHaveBeenCalledTimes(2);
	});
	it("bounds retained entries rather than keeping every visited PR in memory", async () => {
		const config = { ...DEFAULT_CONFIG, hostId: "bounded-cache-host" };
		vi.mocked(getSessionPR).mockResolvedValue([]);
		const first = await mount([{ config, sessionId: "first" }]);
		act(() => first.unmount());
		const bulk = await mount(Array.from({ length: 256 }, (_, index) => ({ config, sessionId: `bulk-${index}` })));
		act(() => bulk.unmount());
		vi.mocked(getSessionPR).mockClear();
		await mount([{ config, sessionId: "first" }]);
		expect(getSessionPR).toHaveBeenCalledTimes(1);
	});
});

describe("PR summary cache identity", () => {
	it("separates the same session ID on two hosts while sharing one host across endpoints", () => {
		const hostA = { ...DEFAULT_CONFIG, hostId: "host-a", host: "192.168.1.10" };
		const hostB = { ...DEFAULT_CONFIG, hostId: "host-b", host: "192.168.1.11" };
		const hostAOverTunnel = { ...hostA, host: "a.example.com", secure: true };

		expect(prSummaryCacheKey(hostA, "same-session")).not.toBe(prSummaryCacheKey(hostB, "same-session"));
		expect(prSummaryCacheKey(hostA, "same-session")).toBe(prSummaryCacheKey(hostAOverTunnel, "same-session"));
	});
});
