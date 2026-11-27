import { AoError } from "../../../packages/shared/telemetry/ao-error";
import type { BaseWindowConstructorOptions, WebContents, WindowOpenHandlerResponse } from "electron";
import { browserWindowViewId } from "../shared/browser-window";
import type { BrowserSessionWindow, BrowserViewHost } from "./browser-view-host";
import type { WindowComposition } from "./window-composition";
import type { BrowserWindowState } from "../shared/browser-window";

type BaseWindowLike = Pick<
	Electron.BaseWindow,
	| "contentView"
	| "getContentBounds"
	| "isDestroyed"
	| "isFocused"
	| "isMinimized"
	| "isVisible"
	| "focus"
	| "show"
	| "restore"
	| "close"
	| "setTitle"
	| "showInactive"
	| "getContentSize"
	| "setContentSize"
	| "isResizable"
	| "setResizable"
> & {
	on(event: "close" | "closed", listener: () => void): unknown;
};

type ShellComposition = Pick<WindowComposition, "restackShell" | "dispose">;

export type BrowserSessionWindows = {
	/** The window.open answer for a Browser window, or null for any other window. */
	handleWindowOpen: (details: { url: string; frameName: string }) => WindowOpenHandlerResponse | null;
	focus: (viewId: string) => void;
	close: (viewId: string) => void;
	/**
	 * Size a session's window for a recording's viewport (scaled to fit the
	 * screen), fixed while it records; null gives back its own size.
	 */
	fitToRecording: (viewId: string, viewport: { width: number; height: number } | null) => void;
	closeAll: () => void;
};

const TITLE = "Browser · Orchestrator";

/**
 * Separate OS windows that each show one session's Browser page.
 *
 * The app shell opens one with window.open("about:blank", name) and renders
 * into it, so its code, state and IPC stay the shell's. This
 * module builds the native window around that document the way the app window
 * is built: a BaseWindow whose shell document sits below the live page views.
 * Closing the window gives the page views back to the app window.
 */
export function createBrowserSessionWindows(options: {
	createWindow: (windowOptions: BaseWindowConstructorOptions) => BaseWindowLike;
	compose: (window: BaseWindowLike, shell: WebContents) => ShellComposition;
	host: Pick<BrowserViewHost, "canAttachWindow" | "attachWindow" | "releaseWindow" | "recordingWindowSize">;
	/** The screen space a window's content can use (the work area less its frame). */
	contentArea: () => { width: number; height: number };
	/**
	 * Tell the app shell a Browser window holds its panel's pages (open), or
	 * closed and gave them back (so the panel docks again).
	 */
	notify: (state: BrowserWindowState) => void;
	windowOptions?: () => Partial<BaseWindowConstructorOptions>;
}): BrowserSessionWindows {
	const windows = new Map<
		string,
		{ window: BaseWindowLike; composition: ShellComposition; beforeRecording?: { width: number; height: number; resizable: boolean } }
	>();

	// The largest content size of the viewport's shape that fits the screen.
	const fitted = (viewport: { width: number; height: number }) => {
		const area = options.contentArea();
		const scale = Math.min(1, area.width / viewport.width, area.height / viewport.height);
		return { width: Math.round(viewport.width * scale), height: Math.round(viewport.height * scale) };
	};

	const release = (viewId: string): void => {
		if (!windows.has(viewId)) return;
		windows.delete(viewId);
		options.host.releaseWindow(viewId);
		options.notify({ viewId, open: false });
	};

	const open = (viewId: string, shell: WebContents): WebContents => {
		// A window a recording asks for is its size, fixed while it records,
		// and appears without taking the keyboard from the person.
		const recording = options.host.recordingWindowSize(viewId);
		const size = recording ? fitted(recording) : undefined;
		const window = options.createWindow({
			...(size
				? { width: size.width, height: size.height, useContentSize: true, resizable: false }
				: { width: 1200, height: 820, minWidth: 480, minHeight: 360 }),
			title: TITLE,
			show: !recording,
			...options.windowOptions?.(),
		});
		if (recording) window.showInactive();
		const composition = options.compose(window, shell);
		windows.set(viewId, { window, composition });
		const sessionWindow: BrowserSessionWindow = {
			get contentView() {
				return window.contentView;
			},
			getContentBounds: () => window.getContentBounds(),
			isDestroyed: () => window.isDestroyed(),
			isFocused: () => window.isFocused(),
			isMinimized: () => window.isMinimized(),
			isVisible: () => window.isVisible(),
			onNativeViewAdded: () => composition.restackShell(),
		};
		// Before the window tears down its views, take the pages back.
		window.on("close", () => release(viewId));
		window.on("closed", () => composition.dispose());
		// The document can go first: the shell reloaded or closed it.
		shell.once("destroyed", () => {
			if (!window.isDestroyed()) window.close();
		});
		// A WebContentsView's document does not name its window on its own.
		shell.on("page-title-updated", (_event, title) => {
			if (!window.isDestroyed()) window.setTitle(title || TITLE);
		});
		if (options.host.attachWindow(viewId, sessionWindow)) {
			options.notify({ viewId, open: true });
		} else {
			// The panel went away while the window was being made.
			windows.delete(viewId);
			window.close();
		}
		return shell;
	};

	return {
		handleWindowOpen: ({ url, frameName }) => {
			const viewId = url === "about:blank" ? browserWindowViewId(frameName) : null;
			if (viewId === null) return null;
			if (windows.has(viewId) || !options.host.canAttachWindow(viewId)) return { action: "deny" };
			return {
				action: "allow",
				// The document is the shell's to fill: it gets no preload or bridge
				// of its own, so the app's IPC still comes only from the app shell.
				overrideBrowserWindowOptions: {
					show: false,
					webPreferences: {
						preload: undefined,
						contextIsolation: true,
						nodeIntegration: false,
						sandbox: true,
						webviewTag: false,
					},
				},
				createWindow: (windowOptions) => {
					// Electron passes the opened document here, though its public
					// type for these options omits the field.
					const shell = (windowOptions as { webContents?: WebContents }).webContents;
					if (!shell) throw new AoError("BROWSER_WINDOW_UNAVAILABLE", "Browser window has no document");
					return open(viewId, shell);
				},
			};
		},
		focus: (viewId) => {
			const window = windows.get(viewId)?.window;
			if (!window || window.isDestroyed()) return;
			if (window.isMinimized()) window.restore();
			window.show();
			window.focus();
		},
		close: (viewId) => {
			const window = windows.get(viewId)?.window;
			if (window && !window.isDestroyed()) window.close();
		},
		fitToRecording: (viewId, viewport) => {
			const entry = windows.get(viewId);
			if (!entry || entry.window.isDestroyed()) return;
			const { window } = entry;
			if (viewport) {
				if (!entry.beforeRecording) {
					const [width, height] = window.getContentSize();
					entry.beforeRecording = { width: width!, height: height!, resizable: window.isResizable() };
				}
				const size = fitted(viewport);
				window.setResizable(false);
				window.setContentSize(size.width, size.height);
				return;
			}
			const before = entry.beforeRecording;
			entry.beforeRecording = undefined;
			if (!before) return;
			window.setResizable(before.resizable);
			window.setContentSize(before.width, before.height);
		},
		closeAll: () => {
			for (const { window } of [...windows.values()]) {
				if (!window.isDestroyed()) window.close();
			}
		},
	};
}
