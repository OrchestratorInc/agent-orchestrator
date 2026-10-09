import { act, render, renderHook } from "@testing-library/react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import {
	getSidebarChromeGeometry,
	setSidebarChromeGeometry,
	subscribeSidebarChromeGeometry,
	useSidebarChromeClearanceRef,
	useSidebarChromeGeometry,
} from "./useSidebarChromeGeometry";

vi.mock("../lib/platform", () => ({ isMacPlatform: () => true }));

type ObserverCallback = (entries: Partial<ResizeObserverEntry>[]) => void;
let observerCallback: ObserverCallback | null = null;
const disconnect = vi.fn();

beforeEach(() => {
	observerCallback = null;
	disconnect.mockClear();
	vi.stubGlobal(
		"ResizeObserver",
		class {
			constructor(callback: ObserverCallback) {
				observerCallback = callback;
			}
			observe() {}
			disconnect = disconnect;
		},
	);
});

afterEach(() => {
	setSidebarChromeGeometry(null);
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

function mountSidebarElements() {
	const host = document.createElement("div");
	host.innerHTML = '<div data-slot="sidebar-gap"></div><div data-slot="sidebar-container"></div>';
	document.body.append(host);
	const [gap, container] = host.children as unknown as HTMLDivElement[];
	vi.spyOn(gap, "getBoundingClientRect").mockReturnValue({ width: 120 } as DOMRect);
	vi.spyOn(container, "getBoundingClientRect").mockReturnValue({ width: 240 } as DOMRect);
	return { host, gap, container };
}

it("observes the new desktop elements after a responsive remount without touching <html>", () => {
	const gapRef = { current: null as HTMLDivElement | null };
	const containerRef = { current: null as HTMLDivElement | null };
	const { rerender, unmount } = renderHook(({ ready }) => useSidebarChromeGeometry(ready, gapRef, containerRef), { initialProps: { ready: false } });
	expect(getSidebarChromeGeometry()).toBeNull();
	const { host, gap, container } = mountSidebarElements();
	gapRef.current = gap;
	containerRef.current = container;
	rerender({ ready: true });
	expect(getSidebarChromeGeometry()).toEqual({ width: 120, progress: 0.5 });
	expect(document.documentElement.style.getPropertyValue("--ao-sidebar-layout-width")).toBe("");
	expect(document.documentElement.style.getPropertyValue("--ao-sidebar-collapse-progress")).toBe("");
	unmount();
	expect(disconnect).toHaveBeenCalled();
	expect(getSidebarChromeGeometry()).toBeNull();
	host.remove();
});

it("updates from ResizeObserver entry sizes without measuring layout again", () => {
	const gapRef = { current: null as HTMLDivElement | null };
	const containerRef = { current: null as HTMLDivElement | null };
	const { host, gap, container } = mountSidebarElements();
	gapRef.current = gap;
	containerRef.current = container;
	renderHook(() => useSidebarChromeGeometry(true, gapRef, containerRef));
	const gapRect = vi.mocked(gap.getBoundingClientRect);
	gapRect.mockClear();
	act(() => observerCallback!([{ target: gap, borderBoxSize: [{ inlineSize: 60, blockSize: 0 }] }]));
	expect(getSidebarChromeGeometry()).toEqual({ width: 60, progress: 0.75 });
	act(() => observerCallback!([{ target: gap, borderBoxSize: [{ inlineSize: 0, blockSize: 0 }] }]));
	expect(getSidebarChromeGeometry()).toEqual({ width: 0, progress: 1 });
	act(() => observerCallback!([{ target: container, borderBoxSize: [{ inlineSize: 300, blockSize: 0 }] }, { target: gap, borderBoxSize: [{ inlineSize: 150, blockSize: 0 }] }]));
	expect(getSidebarChromeGeometry()).toEqual({ width: 150, progress: 0.5 });
	expect(gapRect).not.toHaveBeenCalled();
	host.remove();
});

it("does nothing when not ready", () => {
	const gapRef = { current: document.createElement("div") };
	const containerRef = { current: document.createElement("div") };
	renderHook(() => useSidebarChromeGeometry(false, gapRef, containerRef));
	expect(getSidebarChromeGeometry()).toBeNull();
	expect(observerCallback).toBeNull();
});

it("store notifies subscribers only on change and stops after unsubscribe", () => {
	const listener = vi.fn();
	const unsubscribe = subscribeSidebarChromeGeometry(listener);
	setSidebarChromeGeometry({ width: 10, progress: 0.9 });
	setSidebarChromeGeometry({ width: 10, progress: 0.9 });
	expect(listener).toHaveBeenCalledTimes(1);
	expect(listener).toHaveBeenLastCalledWith({ width: 10, progress: 0.9 });
	unsubscribe();
	setSidebarChromeGeometry({ width: 20, progress: 0.8 });
	expect(listener).toHaveBeenCalledTimes(1);
});

function Header({ enabled = true }: { enabled?: boolean }) {
	const ref = useSidebarChromeClearanceRef<HTMLDivElement>(enabled);
	return <div data-testid="header" ref={ref} />;
}

it("clearance ref initializes from the store, follows updates, and clears on unmount", () => {
	setSidebarChromeGeometry({ width: 100, progress: 0.25 });
	const { getByTestId, unmount } = render(<Header />);
	const header = getByTestId("header");
	expect(header.style.getPropertyValue("--ao-sidebar-layout-width")).toBe("100px");
	expect(header.style.getPropertyValue("--ao-sidebar-collapse-progress")).toBe("0.25");
	act(() => setSidebarChromeGeometry({ width: 0, progress: 1 }));
	expect(header.style.getPropertyValue("--ao-sidebar-layout-width")).toBe("0px");
	expect(header.style.getPropertyValue("--ao-sidebar-collapse-progress")).toBe("1");
	act(() => setSidebarChromeGeometry(null));
	expect(header.style.getPropertyValue("--ao-sidebar-layout-width")).toBe("");
	const listener = vi.fn();
	const probe = subscribeSidebarChromeGeometry(listener);
	unmount();
	act(() => setSidebarChromeGeometry({ width: 5, progress: 0.5 }));
	expect(header.style.getPropertyValue("--ao-sidebar-layout-width")).toBe("");
	probe();
});

it("clearance ref stays inert when disabled and unsubscribes when disabled later", () => {
	setSidebarChromeGeometry({ width: 100, progress: 0.25 });
	const { getByTestId, rerender } = render(<Header enabled={false} />);
	expect(getByTestId("header").style.getPropertyValue("--ao-sidebar-layout-width")).toBe("");
	rerender(<Header enabled />);
	expect(getByTestId("header").style.getPropertyValue("--ao-sidebar-layout-width")).toBe("100px");
	rerender(<Header enabled={false} />);
	expect(getByTestId("header").style.getPropertyValue("--ao-sidebar-layout-width")).toBe("");
});
