// Boot ordering for the desktop shell: the daemon must be up before the main
// window exists. The board cannot render until the daemon answers, and a
// fire-and-forget start raced the first paint — a rebuild launch could show a
// dead board (or none) before the daemon ever appeared.

export const DAEMON_BOOT_TIMEOUT_MS = 15_000;

/**
 * Start the daemon and open the main window in a deterministic order.
 *
 * Non-Windows (macOS/Linux): start the daemon and give it a bounded wait to
 * settle (ready or failed) before the window opens, so the first paint lands
 * on a live board. If the start wedges past the timeout the window opens
 * anyway — the daemon keeps starting in the background.
 *
 * Windows keeps the legacy order (window first, then daemon) until that path
 * has been tried there.
 */
export async function bootDaemonAndWindow({
	startDaemon,
	createWindow,
	isWindows,
	timeoutMs = DAEMON_BOOT_TIMEOUT_MS,
	shouldAbort,
}: {
	startDaemon: () => Promise<unknown>;
	createWindow: () => Promise<void>;
	isWindows: boolean;
	timeoutMs?: number;
	shouldAbort?: () => boolean;
}): Promise<void> {
	if (!isWindows) {
		await Promise.race([
			startDaemon(),
			new Promise<void>((resolve) => setTimeout(resolve, timeoutMs)),
		]);
	}
	await createWindow();
	if (shouldAbort?.()) return;
	if (isWindows) void startDaemon();
}
