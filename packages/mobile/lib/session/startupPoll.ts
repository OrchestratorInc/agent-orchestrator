/**
 * How often a starting session is re-read while its screen is open. The
 * board's poll (8s direct, 2s over the tunnel) skips whole setup stages: fetch
 * takes about half a second and the agent step about one. Kept free of React
 * Native so tests can import it.
 */
export const STARTUP_POLL_MS = 1_000;
