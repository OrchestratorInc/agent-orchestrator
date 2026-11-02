/**
 * How long a failed code must be out of the camera's view before seeing it
 * again counts as a fresh scan.
 *
 * The camera reports a code in frame many times a second, so a failed code that
 * simply stays in view must not retry on every frame. A gap this long only
 * happens when the user points away and back, or the camera stopped
 * reporting for a moment.
 */
export const RESCAN_GAP_MS = 1_000;

export type ScanGate = {
	/** Whether this sighting of a parsed code should start a pairing attempt. */
	sighting(code: string, now: number): boolean;
	/** Claims the scanner for an attempt; false while one is running or done. */
	start(): boolean;
	failed(code: string, now: number, options?: { awaitedLocalNetwork?: boolean }): void;
	/** The user backed out (e.g. dismissed the name sheet): start clean. */
	cancelled(): void;
	succeeded(): void;
	/**
	 * Ends an attempt that stopped without reporting how (it threw), so the
	 * scanner is not left claimed. True if one was still running.
	 */
	release(): boolean;
	/**
	 * The code to retry because the Local Network prompt was answered after
	 * the attempt waiting on it had already given up; at most once per failure.
	 */
	promptAnswered(): string | null;
};

/**
 * Decides when the pair screen's scanner starts an attempt.
 *
 * Lives here rather than in the pair screen because `packages/mobile` has no
 * screen tests.
 *
 * The scanner used to stop after one attempt, so once a scan failed — on a
 * fresh iOS install, the first one always does, because the request raises the
 * Local Network prompt instead of leaving the phone — scanning again did nothing
 * until the screen was reopened.
 */
export function createScanGate(): ScanGate {
	let inFlight = false;
	let done = false;
	let failedCode: string | null = null;
	let lastSeenAt = 0;
	let awaitedLocalNetwork = false;

	return {
		sighting(code, now) {
			if (inFlight || done) return false;
			if (code !== failedCode) return true;
			const away = now - lastSeenAt;
			lastSeenAt = now;
			return away >= RESCAN_GAP_MS;
		},
		start() {
			if (inFlight || done) return false;
			inFlight = true;
			return true;
		},
		failed(code, now, options) {
			inFlight = false;
			failedCode = code;
			lastSeenAt = now;
			awaitedLocalNetwork = options?.awaitedLocalNetwork ?? false;
		},
		cancelled() {
			inFlight = false;
			failedCode = null;
			awaitedLocalNetwork = false;
		},
		succeeded() {
			inFlight = false;
			done = true;
		},
		release() {
			if (!inFlight) return false;
			inFlight = false;
			return true;
		},
		promptAnswered() {
			if (inFlight || done || !awaitedLocalNetwork || failedCode === null) return null;
			awaitedLocalNetwork = false;
			return failedCode;
		},
	};
}
