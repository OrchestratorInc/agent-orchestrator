import { finishUpdateQuit } from "./update-quit";

type QuitEvent = { preventDefault(): void };

/**
 * Quit is only ever deferred, never swallowed: the first before-quit starts
 * the browser-quit cleanup once and arms the update-restart deadline, so a
 * hung dispose cannot leave an unkillable process behind. When the cleanup
 * settles (or the deadline fires), quitting finishes for real.
 */
export function createDesktopQuitController(options: {
	cleanup(): Promise<unknown>;
	finish(): void;
	exit(): void;
	log(error: unknown): void;
}) {
	let armed = false;

	return {
		handleBeforeQuit(event: QuitEvent): void {
			event.preventDefault();
			if (armed) return;
			armed = true;
			void finishUpdateQuit(options.cleanup(), { quit: options.finish, exit: options.exit, log: options.log });
		},
	};
}
