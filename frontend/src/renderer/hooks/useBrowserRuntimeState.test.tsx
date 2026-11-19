import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserRuntimeState } from "../../main/browser-view-host";
import { useBrowserRuntimeState } from "./useBrowserRuntimeState";

const original = window.ao;
const DISCONNECTED: BrowserRuntimeState = { connected: false, connecting: false, rejected: false };
const CONNECTED: BrowserRuntimeState = { connected: true, connecting: false, rejected: false };
const REJECTED: BrowserRuntimeState = { connected: false, connecting: false, rejected: true };

function installBridge(read: () => Promise<BrowserRuntimeState>) {
	const listeners = new Set<(state: BrowserRuntimeState) => void>();
	const browser = {
		...window.ao!.browser,
		getRuntimeState: vi.fn(read),
		reconnectRuntime: vi.fn(async () => undefined),
		onRuntimeState: vi.fn((listener: (state: BrowserRuntimeState) => void) => {
			listeners.add(listener);
			return () => listeners.delete(listener);
		}),
	};
	window.ao = { ...window.ao!, browser };
	return {
		browser,
		listeners,
		push: (state: BrowserRuntimeState) => listeners.forEach((listener) => listener(state)),
	};
}

afterEach(() => {
	window.ao = original;
});

describe("useBrowserRuntimeState", () => {
	it("hydrates a late-mounted panel and follows pushes", async () => {
		const bridge = installBridge(async () => DISCONNECTED);
		const { result, unmount } = renderHook(() => useBrowserRuntimeState());

		expect(result.current).toBeNull();
		await waitFor(() => expect(result.current).toEqual(DISCONNECTED));
		act(() => bridge.push(REJECTED));
		expect(result.current).toEqual(REJECTED);

		unmount();
		expect(bridge.listeners.size).toBe(0);
	});

	it("never lets a late snapshot replace a newer push", async () => {
		let resolveRead: (state: BrowserRuntimeState) => void = () => undefined;
		const bridge = installBridge(
			() =>
				new Promise<BrowserRuntimeState>((resolve) => {
					resolveRead = resolve;
				}),
		);
		const { result, unmount } = renderHook(() => useBrowserRuntimeState());

		act(() => bridge.push(CONNECTED));
		await act(async () => resolveRead(REJECTED));

		expect(result.current).toEqual(CONNECTED);
		unmount();
	});

	it("shares one subscription between panels", () => {
		const bridge = installBridge(async () => CONNECTED);
		const first = renderHook(() => useBrowserRuntimeState());
		const second = renderHook(() => useBrowserRuntimeState());

		expect(bridge.browser.onRuntimeState).toHaveBeenCalledOnce();
		first.unmount();
		expect(bridge.listeners.size).toBe(1);
		second.unmount();
		expect(bridge.listeners.size).toBe(0);
	});
});
