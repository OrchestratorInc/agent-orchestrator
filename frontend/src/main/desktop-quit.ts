type QuitEvent = { preventDefault(): void };

// How long an outside quit may spend on cleanup before the process exits anyway.
// Homebrew's `uninstall quit:` and logout give up on an app within seconds.
export const EXTERNAL_QUIT_DEADLINE_MS = 5_000;

// On macOS with the tray, AO's own Quit (app menu, ⌘Q, the Quit command) only
// closes the window and leaves the Dock; the tray and the daemon keep running.
// Every other quit request — a quit Apple Event from `osascript`, Homebrew,
// the Dock menu or logout, and SIGTERM — exits the process. Electron routes all
// of them through the same `before-quit`, so the in-app Quit marks itself
// before calling app.quit() and anything unmarked counts as an outside quit.
export function createDesktopQuitController(options: {
	platform: NodeJS.Platform;
	hasTray(): boolean;
	isUpdateRestartRequested(): boolean;
	hasWindow(): boolean;
	closeWindow(): void;
	hideDock(): void;
	quit(): void;
	/** An outside quit is about to exit the process (agents should be kept). */
	externalQuitStarted?(): void;
	/** Ends the process without waiting for cleanup. */
	exit(): void;
	externalQuitDeadlineMs?: number;
}) {
	let quitCompletelyRequested = false;
	let backgroundQuitRequested = false;
	let hideDockOnWindowClosed = false;
	let deadline: ReturnType<typeof setTimeout> | undefined;

	const clearDeadline = () => {
		if (deadline) clearTimeout(deadline);
		deadline = undefined;
	};

	return {
		handleBeforeQuit(event: QuitEvent): boolean {
			if (
				options.platform !== "darwin" ||
				!options.hasTray() ||
				quitCompletelyRequested ||
				options.isUpdateRestartRequested()
			) return false;

			if (!backgroundQuitRequested) {
				// Not AO's own Quit: really exit, and never hang the caller.
				quitCompletelyRequested = true;
				options.externalQuitStarted?.();
				clearDeadline();
				deadline = setTimeout(() => {
					deadline = undefined;
					options.exit();
				}, options.externalQuitDeadlineMs ?? EXTERNAL_QUIT_DEADLINE_MS);
				return false;
			}
			backgroundQuitRequested = false;

			event.preventDefault();
			if (options.hasWindow()) {
				// Leave the Dock once the window is really gone: the draft guard can
				// still cancel the close.
				hideDockOnWindowClosed = true;
				// close(), not destroy(): the existing unsaved-draft guard still runs.
				options.closeWindow();
			} else {
				options.hideDock();
			}
			return true;
		},
		/** AO's own Quit: on macOS with the tray this only closes the window. */
		backgroundQuit(): void {
			backgroundQuitRequested = true;
			try {
				options.quit();
			} finally {
				// before-quit runs synchronously inside app.quit(); never let the mark
				// leak onto a later outside quit.
				backgroundQuitRequested = false;
			}
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
			clearDeadline();
		},
	};
}
