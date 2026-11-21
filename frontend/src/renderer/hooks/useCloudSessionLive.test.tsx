import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpSessionLive } from "../lib/cloud-cp";
import { useCloudSessionLive } from "./useCloudSessionLive";

type StreamOptions = { live?: boolean; types?: string[]; onLive: (state: CloudCpSessionLive) => void; onOpen?: () => void; signal: AbortSignal };
const streams = vi.hoisted(() => [] as StreamOptions[]);
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ baseUrl: "https://cp.test", ready: true }) }));
vi.mock("../lib/cloud-cp/stream-bridge", () => ({
	subscribeSessionEventsBridged: (options: StreamOptions) => {
		streams.push(options);
		return new Promise<void>((resolve) => options.signal.addEventListener("abort", () => resolve()));
	},
}));

const frame: CloudCpSessionLive = {
	type: "session.live", viewers: [], composer: { holder: null, epoch: 0, waitlist: [] },
	canTakeOver: false, you: "user-1", chatSettings: {}, canConfigure: false,
};

describe("useCloudSessionLive", () => {
	it("opens one live-only stream for every component showing the session, and closes it after the last", async () => {
		streams.length = 0;
		const chat = renderHook(() => useCloudSessionLive("org-1", "session-1", true));
		const header = renderHook(() => useCloudSessionLive("org-1", "session-1", true));
		await waitFor(() => expect(streams).toHaveLength(1));
		expect(streams[0]).toMatchObject({ live: true, types: ["session.live"] });

		act(() => streams[0].onLive(frame));
		expect(chat.result.current).toEqual(frame);
		expect(header.result.current).toEqual(frame);

		chat.unmount();
		await new Promise((resolve) => setTimeout(resolve, 5));
		expect(streams[0].signal.aborted).toBe(false);
		header.unmount();
		expect(streams[0].signal.aborted).toBe(true);
		expect(streams).toHaveLength(1);
	});

	it("opens nothing while disabled", async () => {
		streams.length = 0;
		const { result } = renderHook(() => useCloudSessionLive("org-1", "session-2", false));
		await new Promise((resolve) => setTimeout(resolve, 5));
		expect(streams).toHaveLength(0);
		expect(result.current).toBeUndefined();
	});
});
