// Like most macOS apps, ⌘Q asks before quitting: the shell shows a "Quit AO?"
// dialog and reports the answer back. A second ⌘Q while that dialog is up quits,
// and choosing Quit from the menu with the mouse never asks.
export function createQuitConfirmController(options: {
	/** False when there is no visible shell to show the dialog in. */
	canConfirm(): boolean;
	requestConfirm(): void;
	quit(): void;
}) {
	let pending = false;

	return {
		menuQuit(fromShortcut: boolean): void {
			if (!fromShortcut || pending || !options.canConfirm()) {
				pending = false;
				options.quit();
				return;
			}
			pending = true;
			options.requestConfirm();
		},
		resolve(confirmed: boolean): void {
			pending = false;
			if (confirmed) options.quit();
		},
		/** The shell reloaded, so any dialog it was showing is gone. */
		reset(): void {
			pending = false;
		},
	};
}
