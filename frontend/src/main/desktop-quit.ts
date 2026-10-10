import type { Dock } from "electron";

type QuitEvent = { preventDefault(): void };

export function createDesktopQuitController(options: {
	platform: NodeJS.Platform;
	hasTray(): boolean;
	isUpdateRestartRequested(): boolean;
	dock?: Pick<Dock, "hide" | "show" | "isVisible">;
	closeWindow(): void;
	quit(): void;
}) {
	let quitCompletelyRequested = false;
	let dockHideTimer: ReturnType<typeof setTimeout> | undefined;
	let dockShowPromise: Promise<void> | undefined;

	function canBackground(): boolean {
		return options.platform === "darwin" && options.hasTray() &&
			!quitCompletelyRequested && !options.isUpdateRestartRequested();
	}

	function cancelDockHide(): void {
		clearTimeout(dockHideTimer);
		dockHideTimer = undefined;
	}

	return {
		handleBeforeQuit(event: QuitEvent): boolean {
			if (!canBackground()) {
				cancelDockHide();
				return false;
			}

			event.preventDefault();
			// close(), not destroy(): the existing unsaved-draft guard still runs.
			options.closeWindow();
			return true;
		},
		handleWindowClosed(): void {
			const dock = options.dock;
			if (!canBackground() || !dock) return;
			cancelDockHide();
			dock.hide();
			if (dock.isVisible()) {
				// Electron ignores hides for one second after show(); retry after that guard.
				dockHideTimer = setTimeout(() => {
					dockHideTimer = undefined;
					if (canBackground()) dock.hide();
				}, 1100);
			}
		},
		beforeWindowShow(): Promise<void> {
			cancelDockHide();
			if (dockShowPromise) return dockShowPromise;
			const dock = options.dock;
			if (options.platform !== "darwin" || !dock || dock.isVisible()) return Promise.resolve();
			dockShowPromise = dock.show().finally(() => { dockShowPromise = undefined; });
			return dockShowPromise;
		},
		quitCompletely(): void {
			quitCompletelyRequested = true;
			cancelDockHide();
			options.quit();
		},
		cancelQuit(): void {
			quitCompletelyRequested = false;
		},
	};
}
