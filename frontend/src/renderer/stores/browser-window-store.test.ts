import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { browserWindowFrameName } from "../../shared/browser-window";
import type { WorkspaceSession } from "../types/workspace";
import { useBrowserWindowStore, type BrowserWindowPanel } from "./browser-window-store";

const panel = (overrides: Partial<BrowserWindowPanel> = {}): BrowserWindowPanel => ({
	session: { id: "sess-1" } as WorkspaceSession,
	terminated: false,
	...overrides,
});

// A same-origin about:blank frame stands in for the window the shell opens.
function childWindow(): Window {
	const frame = document.createElement("iframe");
	document.body.appendChild(frame);
	return frame.contentWindow!;
}

describe("useBrowserWindowStore", () => {
	let open: ReturnType<typeof vi.spyOn>;
	let child: Window;

	beforeEach(() => {
		child = childWindow();
		open = vi.spyOn(window, "open").mockImplementation(() => child as never);
		window.ao!.browser.focusWindow = vi.fn(async () => undefined);
		window.ao!.browser.closeWindow = vi.fn(async () => undefined);
	});

	afterEach(() => {
		open.mockRestore();
		useBrowserWindowStore.setState({ windows: {} });
		document.body.querySelectorAll("iframe").forEach((node) => node.remove());
	});

	it("opens the session's window by its view and renders once it holds the pages", () => {
		const store = useBrowserWindowStore.getState();
		expect(store.openWindow("sess-1", "1:sess-1", panel())).toBe(true);
		expect(open).toHaveBeenCalledWith("about:blank", browserWindowFrameName("1:sess-1"), expect.any(String));
		expect(useBrowserWindowStore.getState().windows["sess-1"]?.container).toBeNull();

		store.applyWindowState({ viewId: "1:sess-1", open: true });

		const entry = useBrowserWindowStore.getState().windows["sess-1"]!;
		expect(entry.container?.ownerDocument).toBe(child.document);
	});

	it("shows the window a session already has instead of opening another", () => {
		const store = useBrowserWindowStore.getState();
		store.openWindow("sess-1", "1:sess-1", panel());
		open.mockClear();
		expect(store.openWindow("sess-1", "1:sess-1", panel())).toBe(true);
		expect(open).not.toHaveBeenCalled();
		expect(window.ao!.browser.focusWindow).toHaveBeenCalledWith("1:sess-1");
	});

	it("says so when the window could not open", () => {
		open.mockReturnValueOnce(null);
		expect(useBrowserWindowStore.getState().openWindow("sess-1", "1:sess-1", panel())).toBe(false);
		expect(useBrowserWindowStore.getState().windows).toEqual({});
	});

	it("forgets the window when it closes", () => {
		const store = useBrowserWindowStore.getState();
		store.openWindow("sess-1", "1:sess-1", panel());
		store.applyWindowState({ viewId: "1:sess-1", open: true });
		store.applyWindowState({ viewId: "1:sess-1", open: false });
		expect(useBrowserWindowStore.getState().windows).toEqual({});
	});

	it("closes a window no panel here asked for", () => {
		useBrowserWindowStore.getState().applyWindowState({ viewId: "1:sess-9", open: true });
		expect(window.ao!.browser.closeWindow).toHaveBeenCalledWith("1:sess-9");
	});

	it("updates the panel only when what it shows changed", () => {
		const store = useBrowserWindowStore.getState();
		const first = panel();
		store.openWindow("sess-1", "1:sess-1", first);
		const before = useBrowserWindowStore.getState().windows;
		store.updatePanel("sess-1", { ...first });
		expect(useBrowserWindowStore.getState().windows).toBe(before);
		store.updatePanel("sess-1", { ...first, previewRevision: 2 });
		expect(useBrowserWindowStore.getState().windows["sess-1"]?.panel?.previewRevision).toBe(2);
	});

	it("opens a window a recording asks for before the session's view has shown it", () => {
		const store = useBrowserWindowStore.getState();
		expect(store.openWindow("sess-1", "1:sess-1")).toBe(true);
		expect(useBrowserWindowStore.getState().windows["sess-1"]?.panel).toBeUndefined();
		// The session's view fills in what its page needs once it shows the session.
		store.updatePanel("sess-1", panel());
		expect(useBrowserWindowStore.getState().windows["sess-1"]?.panel?.session.id).toBe("sess-1");
	});
});
