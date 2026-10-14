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

	return {
		handleBeforeQuit(event: QuitEvent): boolean {
			if (
				options.platform !== "darwin" ||
				!options.hasTray() ||
				quitCompletelyRequested ||
				options.isUpdateRestartRequested()
			) return false;

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
