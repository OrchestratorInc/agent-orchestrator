import { create } from "zustand";
import { browserWindowFrameName, type BrowserWindowState } from "../../shared/browser-window";
import { prepareBrowserWindowDocument } from "../lib/browser-window-document";
import type { WorkspaceSession } from "../types/workspace";

/**
 * What the Browser panel of a session needs, as the session's view in the app
 * window last knew it. That view publishes it while it shows the session; the
 * window keeps the last of it while the app window shows another session.
 */
export type BrowserWindowPanel = {
	session: WorkspaceSession;
	hostId?: string;
	origin?: { hostId: string; sessionId: string; proxyBase: string };
	cloudAgentBrowser?: { baseUrl: string; orgId: string; sessionId: string };
	previewUrl?: string;
	previewRevision?: number;
	terminated: boolean;
	sessionEnded?: boolean;
};

export type BrowserWindowEntry = {
	viewId: string;
	/** The window.open handle of the window's document. */
	child: Window;
	/** Where the page's slot renders, once the window holds the session's pages. */
	container: HTMLElement | null;
	/** Unknown until the session's view in the app window shows it (a recording can open the window first). */
	panel?: BrowserWindowPanel;
	dispose?: () => void;
};

type BrowserWindowStore = {
	/** By the session's UI id (the one its Browser view is made for). */
	windows: Record<string, BrowserWindowEntry>;
	/** Open the session's Browser in its own window, or show the one it has. */
	openWindow: (uiSessionId: string, viewId: string, panel?: BrowserWindowPanel) => boolean;
	updatePanel: (uiSessionId: string, panel: BrowserWindowPanel) => void;
	/** The main process reports a window holding (or giving back) its pages. */
	applyWindowState: (state: BrowserWindowState) => void;
};

const WINDOW_FEATURES = "width=1200,height=820";

/**
 * A session's Browser page in its own OS window. The window is a document
 * this app shell opened (browser-session-windows.ts makes the native window
 * around it); BrowserWindowsHost renders each page into its window.
 */
export const useBrowserWindowStore = create<BrowserWindowStore>((set, get) => ({
	windows: {},
	openWindow: (uiSessionId, viewId, panel) => {
		const existing = get().windows[uiSessionId];
		if (existing) {
			void window.ao?.browser.focusWindow(existing.viewId);
			return true;
		}
		const child = window.open("about:blank", browserWindowFrameName(viewId), WINDOW_FEATURES);
		if (!child) return false;
		set((state) => ({ windows: { ...state.windows, [uiSessionId]: { viewId, child, container: null, panel } } }));
		return true;
	},
	updatePanel: (uiSessionId, panel) => {
		const entry = get().windows[uiSessionId];
		if (!entry || (entry.panel && samePanel(entry.panel, panel))) return;
		set((state) => ({ windows: { ...state.windows, [uiSessionId]: { ...entry, panel } } }));
	},
	applyWindowState: ({ viewId, open }) => {
		const found = Object.entries(get().windows).find(([, entry]) => entry.viewId === viewId);
		if (!found) {
			// Not this document's window (one left over from before a reload).
			if (open) void window.ao?.browser.closeWindow(viewId);
			return;
		}
		const [uiSessionId, entry] = found;
		if (!open) {
			entry.dispose?.();
			set((state) => {
				const { [uiSessionId]: _closed, ...rest } = state.windows;
				return { windows: rest };
			});
			return;
		}
		if (entry.container || entry.child.closed) return;
		const prepared = prepareBrowserWindowDocument(entry.child);
		set((state) => ({
			windows: { ...state.windows, [uiSessionId]: { ...entry, container: prepared.container, dispose: prepared.dispose } },
		}));
	},
}));

function samePanel(a: BrowserWindowPanel, b: BrowserWindowPanel): boolean {
	return (
		a.session === b.session &&
		a.hostId === b.hostId &&
		a.origin?.hostId === b.origin?.hostId &&
		a.origin?.sessionId === b.origin?.sessionId &&
		a.origin?.proxyBase === b.origin?.proxyBase &&
		a.cloudAgentBrowser?.baseUrl === b.cloudAgentBrowser?.baseUrl &&
		a.cloudAgentBrowser?.orgId === b.cloudAgentBrowser?.orgId &&
		a.cloudAgentBrowser?.sessionId === b.cloudAgentBrowser?.sessionId &&
		a.previewUrl === b.previewUrl &&
		a.previewRevision === b.previewRevision &&
		a.terminated === b.terminated &&
		a.sessionEnded === b.sessionEnded
	);
}
