// Pure decision helper for the wedged-orphan kill+replace path.
//
// Context: on app launch, after both attach attempts fail (inspectExistingDaemon
// and resolveDaemonFromPort both returned null/non-ready), a process may still
// be holding the daemon port. Spawning a new daemon then makes the Go child
// collide on the port and exit 1. This helper encodes the decision: kill the
// holder when the run-file names a PID that is still alive (a hung/wedged holder
// that bound the port but is not answering /healthz). The probe disjunct
// (probe !== null) is kept as a defensive guard only: by the time this helper
// is called, resolveDaemonFromPort already returned null, so a holder that
// answers /healthz at this point is unexpected. If it does happen (e.g. a race),
// we still replace it rather than colliding on spawn.
//
// Kept side-effect free and dependency-injected (no node:* or electron imports)
// so it can be exercised in vitest without the Electron polyfill layer.

import type { DaemonProbe } from "./daemon-attach";

/**
 * Reports whether something is holding the daemon port that we must kill before
 * spawning. By the time it is called, both healthy-reuse attach paths have already
 * returned null. The primary trigger is holderPidAlive: the run-file names a PID
 * that is still alive but is not answering /healthz (a hung/wedged holder). The
 * probe disjunct is a defensive guard: a holder that answers at this point is
 * unexpected (resolveDaemonFromPort already returned null), but if it does appear,
 * we replace it rather than colliding on spawn.
 *
 * Returns true when the caller should kill the holder, wait for the port to
 * free, clear the stale run-file, then spawn a fresh daemon.
 *
 * Returns false when there is no detectable holder; spawn immediately.
 *
 * ponytail: two-condition OR covers the entire decision surface; the probe's
 * content (pid, executablePath) is for the caller's kill logic, not ours.
 */
export function shouldReplacePortHolder(probe: DaemonProbe | null, holderPidAlive: boolean): boolean {
	return probe !== null || holderPidAlive;
}

export type BrowserDaemonOwnershipDecision =
	| { action: "attach" }
	| { action: "replace"; keepAlive: boolean };

/**
 * A daemon belongs to the Electron launch that reported its app run id: reuse
 * within that launch is safe, and across launches we replace it gracefully,
 * preserving the original persistence mode (app-owned daemons remain app-owned;
 * persistent/headless daemons remain alive after the window closes).
 *
 * This is a daemon ownership/lifecycle decision, not an authentication one. The
 * browser runtime token is derived from the data dir and survives an app restart
 * (see browser-runtime-token.ts), so a kept-alive daemon still accepts this app
 * after a relaunch; replacing it no longer exists to repair a token mismatch.
 */
export function browserDaemonOwnershipDecision(
	currentAppRunId: string,
	existing: { owner?: string; appRunId?: string },
): BrowserDaemonOwnershipDecision {
	if (existing.appRunId === currentAppRunId) return { action: "attach" };
	return { action: "replace", keepAlive: existing.owner !== "app" };
}

export type BrowserRuntimeRecoveryDecision =
	| { action: "wait" }
	| { action: "replace"; reason: string };

/**
 * The daemon closed the link without accepting the hello. A current broker
 * explains itself with helloRejected; a daemon predating that frame just hangs
 * up, and both mean the same thing to us.
 */
export const HANDSHAKE_CLOSED_BEFORE_ACCEPT = "closed-before-accept";

/**
 * Refusals a retry cannot repair. A daemon that rejects this app's token, or
 * drops the link before accepting it, would refuse identically forever, so the
 * daemon itself has to be retired. Anything else (a version skew, a daemon that
 * never answered) may resolve without killing a healthy daemon.
 */
const UNRECOVERABLE_HANDSHAKE_REASONS: ReadonlySet<string> = new Set([
	"invalid-token",
	HANDSHAKE_CLOSED_BEFORE_ACCEPT,
]);

/**
 * A daemon the app merely adopted holds the runtime token from whoever started
 * it, so an unrecoverable refusal cannot be retried away: it means this daemon
 * can never drive our browser panel for the rest of the launch. Retiring it and
 * spawning our own is the only recovery, and it is limited to that case.
 *
 * Waits when the app spawned the daemon itself (our token was handed to it, so a
 * refusal means something is wrong that a restart would not fix), when the
 * refusal is recoverable, and after one attempt per launch so a daemon that keeps
 * refusing cannot be replaced in a loop.
 */
export function browserRuntimeRecoveryDecision(input: {
	rejectedReason: string | null;
	spawnedHere: boolean;
	attempted: boolean;
}): BrowserRuntimeRecoveryDecision {
	if (input.rejectedReason === null) return { action: "wait" };
	if (!UNRECOVERABLE_HANDSHAKE_REASONS.has(input.rejectedReason)) return { action: "wait" };
	if (input.spawnedHere) return { action: "wait" };
	if (input.attempted) return { action: "wait" };
	return { action: "replace", reason: input.rejectedReason };
}

/**
 * Whether the startup path must retire an existing daemon instead of attaching.
 *
 * `forceRequested` is sticky by design: the renderer's first daemon.getStatus()
 * can attach while boot is still awaiting the login-shell environment, so a
 * forced replacement requested during that window must be honored by the startup
 * already in flight rather than dropped by the memoized promise.
 */
export function shouldReplaceDaemonForStartup(input: {
	ownership: BrowserDaemonOwnershipDecision;
	forceRequested: boolean;
}): boolean {
	return input.forceRequested || input.ownership.action === "replace";
}

/**
 * The persistence mode to hand the replacement. A forced replacement adopts no
 * lifetime guidance from the daemon it retires (it may not even have one), so it
 * keeps the daemon alive exactly like a forced keep-alive launch.
 */
export function keepAliveForReplacement(input: {
	ownership: BrowserDaemonOwnershipDecision;
	forceRequested: boolean;
}): boolean {
	return input.forceRequested ? true : input.ownership.action === "replace" && input.ownership.keepAlive;
}
