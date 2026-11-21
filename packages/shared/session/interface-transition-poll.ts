/**
 * The interface-transition status poll schedule shared by AO's desktop and
 * mobile clients. Each client decides what its status means (active phase,
 * recovery fence, readiness codes) and how fast it polls a live handoff; this
 * owns the budgets and the backoff so the two cannot drift apart.
 */

export const nativeSessionReadinessPoll = 1_000;

// The readiness recheck is bounded because its codes are not guaranteed to
// clear at all: a `--resume` relaunch stays UNVERIFIED indefinitely (#4122).
// The budget counts ANSWERS for one readiness state; a new state earns a fresh
// window, and a timeout never spends it (that is the failure budget's job).
export const nativeSessionReadinessAttempts = 10;

// Consecutive failures absorbed when no handoff is live. A live handoff keeps
// retrying on the backoff, because capping it stranded the banner behind any
// outage longer than the budget (#4852 review).
export const speculativeFailureAttempts = 5;

export const interfaceTransitionFailureBackoff = [1_000, 2_000, 4_000, 8_000] as const;

/** Milliseconds until the next status poll, or `undefined` to stop polling. */
export function interfaceTransitionPollDelay(args: {
	/** A handoff is fenced on recovery (TARGET_STOP_UNCONFIRMED): never poll. */
	blocked: boolean;
	/** A handoff is in an active phase. */
	live: boolean;
	/** How often this client polls a live handoff. */
	livePollMs: number;
	/** The status is a transient readiness state worth rechecking. */
	readinessPending: boolean;
	/** Answers already spent on the current readiness state. */
	readinessAttempts: number;
	/** Consecutive failed requests since the last one that landed. */
	consecutiveFailures: number;
	/** The daemon said the session is gone (404/410). */
	sessionGone?: boolean;
}): number | undefined {
	if (args.blocked) return undefined;
	const failures = args.consecutiveFailures;
	if (failures > 0) {
		if (args.sessionGone) return undefined;
		if (!args.live && failures >= speculativeFailureAttempts) return undefined;
		return interfaceTransitionFailureBackoff[Math.min(failures, interfaceTransitionFailureBackoff.length) - 1];
	}
	if (args.live) return args.livePollMs;
	if (args.readinessPending && args.readinessAttempts < nativeSessionReadinessAttempts) {
		return nativeSessionReadinessPoll;
	}
	return undefined;
}
