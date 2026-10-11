type QuitEvent = { preventDefault(): void };

export function createDesktopQuitController(options: {
	platform: NodeJS.Platform;
	hasTray(): boolean;
	isUpdateRestartRequested(): boolean;
	closeWindow(): void;
	quit(): void;
}) {
	let quitCompletelyRequested = false;

	return {
		handleBeforeQuit(event: QuitEvent): boolean {
			if (
				options.platform !== "darwin" ||
				!options.hasTray() ||
				quitCompletelyRequested ||
				options.isUpdateRestartRequested()
			) return false;

			event.preventDefault();
			// close(), not destroy(): the existing unsaved-draft guard still runs.
			options.closeWindow();
			return true;
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
