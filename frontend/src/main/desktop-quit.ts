type QuitEvent = { preventDefault(): void };

// How long an outside quit may spend on cleanup before the process exits anyway.
// Homebrew's `uninstall quit:` gives up on an app within seconds.
export const EXTERNAL_QUIT_DEADLINE_MS = 5_000;

/**
 * Controls macOS quit requests while the tray and daemon keep running.
 * Orchestrator's own Quit (app menu, ⌘Q, the Quit command) closes the window
 * and leaves the Dock. Every other quit request (a quit Apple Event from
 * `osascript`, Homebrew or the Dock menu, and SIGTERM) exits the process.
 * Logout and shutdown are handled by the powerMonitor "shutdown" listener,
 * which calls quitCompletely().
 * Electron routes them all through `before-quit`, so the in-app Quit marks
 * itself before app.quit() and anything unmarked counts as an outside quit.
 */
export function createDesktopQuitController(options: {
	platform: NodeJS.Platform;
	hasTray(): boolean;
	isUpdateRestartRequested(): boolean;
	hasWindow(): boolean;
	closeWindow(): void;
	hideDock(): void;
	quit(): void;
	/** An outside quit is about to exit the process (agents should be kept). */
	externalQuitStarted(): void;
	/** Ends the process without waiting for cleanup. */
	exit(): void;
	externalQuitDeadlineMs?: number;
}) {
	let quitCompletelyRequested = false;
	let externalQuitRequested = false;
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
				// Not Orchestrator's own Quit: really exit, and never hang the caller.
				quitCompletelyRequested = true;
				externalQuitRequested = true;
				options.externalQuitStarted();
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
		/** Start the outside-quit budget after the synchronous draft guard passes. */
		startCleanupDeadline(): void {
			if (!externalQuitRequested || deadline !== undefined) return;
			deadline = setTimeout(() => {
				options.exit();
			}, options.externalQuitDeadlineMs ?? EXTERNAL_QUIT_DEADLINE_MS);
		},
		/** Orchestrator's own Quit: on macOS with the tray this only closes the window. */
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
		/**
		 * The window's draft guard kept the window open, which also cancels any
		 * quit in progress. Returns true when that was a full or outside quit, so
		 * the caller can undo its quit-only state (agent preservation).
		 */
		windowCloseCanceled(): boolean {
			hideDockOnWindowClosed = false;
			const quitWasPending = quitCompletelyRequested;
			// Never force an exit the person declined, and let the next in-app
			// Quit background the app again.
			quitCompletelyRequested = false;
			externalQuitRequested = false;
			clearDeadline();
			return quitWasPending;
		},
		quitCompletely(): void {
			quitCompletelyRequested = true;
			options.quit();
		},
		cancelQuit(): void {
			quitCompletelyRequested = false;
			externalQuitRequested = false;
			clearDeadline();
		},
	};
}
