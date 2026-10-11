import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { BrowserRuntimeState } from "../../main/browser-view-host";
import { reconnectBrowserRuntime, useBrowserRuntimeState } from "./useBrowserRuntimeState";

const original = window.ao;

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
		const bridge = installBridge(async () => ({ connected: false, rejected: false }));
		const { result, unmount } = renderHook(() => useBrowserRuntimeState());

		await waitFor(() => expect(result.current).toEqual({ connected: false, rejected: false }));
		act(() => bridge.push({ connected: false, rejected: true }));
		expect(result.current).toEqual({ connected: false, rejected: true });

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

		act(() => bridge.push({ connected: true, rejected: false }));
		await act(async () => resolveRead({ connected: false, rejected: true }));

		expect(result.current).toEqual({ connected: true, rejected: false });
		unmount();
	});

	it("shares one subscription between panels", () => {
		const bridge = installBridge(async () => ({ connected: true, rejected: false }));
		const first = renderHook(() => useBrowserRuntimeState());
		const second = renderHook(() => useBrowserRuntimeState());

		expect(bridge.browser.onRuntimeState).toHaveBeenCalledOnce();
		first.unmount();
		second.unmount();
	});

	it("asks the main process to relink", async () => {
		const bridge = installBridge(async () => ({ connected: false, rejected: true }));
		await reconnectBrowserRuntime();
		expect(bridge.browser.reconnectRuntime).toHaveBeenCalledOnce();
	});
});
