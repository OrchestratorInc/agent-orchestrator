import { act, renderHook } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import type { CloudCpSessionLive } from "../lib/cloud-cp";
import { useCloudSessionLive } from "./useCloudSessionLive";

const streams = vi.hoisted(() => [] as { onLive: (state: CloudCpSessionLive) => void; signal: AbortSignal }[]);
vi.mock("./useCloudCp", () => ({ useCloudCp: () => ({ baseUrl: "https://cp.test", ready: true }) }));
vi.mock("../lib/cloud-cp/stream-bridge", () => ({
	subscribeSessionLiveBridged: ({ onLive, signal }: { onLive: (state: CloudCpSessionLive) => void; signal: AbortSignal }) => {
		streams.push({ onLive, signal });
		return new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve()));
	},
}));

const frame: CloudCpSessionLive = {
	type: "session.live", viewers: [], composer: { holder: null, epoch: 0, waitlist: [] },
	canTakeOver: false, you: "user-1", chatSettings: {}, canConfigure: false,
};

describe("useCloudSessionLive", () => {
	it("opens one stream for every component showing the session, and closes it after the last", () => {
		streams.length = 0;
		const chat = renderHook(() => useCloudSessionLive("org-1", "session-1", true));
		const header = renderHook(() => useCloudSessionLive("org-1", "session-1", true));
		expect(streams).toHaveLength(1);

		act(() => streams[0].onLive(frame));
		expect(chat.result.current).toEqual(frame);
		expect(header.result.current).toEqual(frame);

		chat.unmount();
		expect(streams[0].signal.aborted).toBe(false);
		header.unmount();
		expect(streams[0].signal.aborted).toBe(true);
	});

	it("opens nothing while disabled", () => {
		streams.length = 0;
		const { result } = renderHook(() => useCloudSessionLive("org-1", "session-2", false));
		expect(streams).toHaveLength(0);
		expect(result.current).toBeUndefined();
	});
});
