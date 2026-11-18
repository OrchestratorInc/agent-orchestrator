import { isTrayEnabled } from "./tray-lifecycle";

type QuitEvent = { preventDefault(): void };

export function createDesktopQuitController(options: {
	platform: NodeJS.Platform;
	hasTray(): boolean;
	isUpdateRestartRequested(): boolean;
	hasWindow(): boolean;
	closeWindow(): void;
	hideDock(): void;
	quit(): void;
}) {
	let quitCompletelyRequested = false;
	let hideDockOnWindowClosed = false;
	let backgroundQuitRequested = false;

	return {
		backgroundQuit(): void {
			// Only AO's own Quit backgrounds on Windows. Installer and OS exits
			// must pass through, including asynchronous cleanup continuations.
			backgroundQuitRequested = true;
			try {
				options.quit();
			} finally {
				backgroundQuitRequested = false;
			}
		},
		handleBeforeQuit(event: QuitEvent): boolean {
			if (
				!isTrayEnabled(options.platform) ||
				(options.platform === "win32" && !backgroundQuitRequested) ||
				!options.hasTray() ||
				quitCompletelyRequested ||
				options.isUpdateRestartRequested()
			) return false;

			event.preventDefault();
			if (options.hasWindow()) {
				// Leave the Dock once the window is really gone: the draft guard can
				// still cancel the close.
				hideDockOnWindowClosed = options.platform === "darwin";
				// close(), not destroy(): the existing unsaved-draft guard still runs.
				options.closeWindow();
			} else if (options.platform === "darwin") {
				options.hideDock();
			}
			return true;
		},
		windowClosed(): void {
			if (!hideDockOnWindowClosed) return;
			hideDockOnWindowClosed = false;
			options.hideDock();
		},
		windowCloseCanceled(): void {
			hideDockOnWindowClosed = false;
		},
		quitCompletely(): void {
			quitCompletelyRequested = true;
			options.quit();
		},
		cancelQuit(): void {
			quitCompletelyRequested = false;
		},
	};
}
