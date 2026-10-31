// How the link between this phone and the machine is doing, from two sources:
// what the phone's OS says about its own network, and how the board poll's
// recent requests went. Free of React Native / Expo imports so it can be
// unit-tested directly, like connectionError.ts.
//
// The rule this exists for: a request that got no answer is not, on its own,
// proof that the machine is offline. A phone with no signal, a radio waking up
// or one dropped packet all look the same from inside fetch. Before this, a
// single failed poll put "Your machine is offline" on screen, and the next
// successful poll quietly took it back.

/** The phone's own network, as the OS reports it. `undefined` is "not known yet". */
export type PhoneNetwork = {
	connected: boolean | undefined;
	internet: boolean | undefined;
};

export const UNKNOWN_PHONE_NETWORK: PhoneNetwork = { connected: undefined, internet: undefined };

/**
 * How one poll went.
 *
 * - ok: answered in good time
 * - slow: answered, but slowly enough that the user would notice
 * - timeout: never answered within the request timeout
 * - no-answer: failed outright (refused, DNS, no route)
 * - answered-error: the machine answered with an error status. The link
 *   worked, so this says nothing about the network.
 */
export type PollOutcome = "ok" | "slow" | "timeout" | "no-answer" | "answered-error";

export type LinkHealth =
	| "ok"
	| "slow" // the machine answers, but the connection is struggling
	| "reconnecting" // nothing is answering, but not for long enough to call it down
	| "phone-offline" // the phone itself has no network
	| "unreachable"; // the phone is online and the machine has stopped answering

/** A poll that took longer than this counts as slow. */
export const SLOW_POLL_MS = 4_000;

/**
 * How many unanswered polls in a row, and for how long, before the machine is
 * called unreachable. Both must hold: over the tunnel a refused request fails
 * in milliseconds and the poll runs every two seconds, so a count alone would
 * still give up within a few seconds of a blip.
 */
export const UNREACHABLE_AFTER_FAILURES = 3;
export const UNREACHABLE_AFTER_MS = 15_000;

/** How many recent polls the slow-connection signal looks back over. */
export const RECENT_POLLS = 5;
/** How many slow or timed-out polls in that window make the connection slow. */
export const SLOW_AFTER = 2;

export type LinkTracker = {
	/** Newest last, at most RECENT_POLLS long. */
	recent: PollOutcome[];
	/** Unanswered polls (timeout or no-answer) in a row. */
	unansweredStreak: number;
	/** When the current unanswered run began, epoch ms. Meaningless while the streak is 0. */
	unansweredSince: number;
};

export const EMPTY_LINK_TRACKER: LinkTracker = { recent: [], unansweredStreak: 0, unansweredSince: 0 };

function unanswered(outcome: PollOutcome): boolean {
	return outcome === "timeout" || outcome === "no-answer";
}

/** Folds one finished poll into the tracker. */
export function recordPoll(tracker: LinkTracker, outcome: PollOutcome, now: number): LinkTracker {
	const recent = [...tracker.recent, outcome].slice(-RECENT_POLLS);
	if (!unanswered(outcome)) return { recent, unansweredStreak: 0, unansweredSince: 0 };
	return {
		recent,
		unansweredStreak: tracker.unansweredStreak + 1,
		unansweredSince: tracker.unansweredStreak === 0 ? now : tracker.unansweredSince,
	};
}

/** True only when the OS says so. An unknown state is not offline. */
export function isPhoneOffline(phone: PhoneNetwork): boolean {
	return phone.connected === false;
}

export function classifyLink(tracker: LinkTracker, phone: PhoneNetwork, now: number): LinkHealth {
	if (isPhoneOffline(phone)) return "phone-offline";
	const last = tracker.recent[tracker.recent.length - 1];
	if (last !== undefined && unanswered(last)) {
		// On a network with no internet, a remote path cannot work. The machine may
		// be fine; it's the phone's connection that needs fixing. A LAN path can
		// still work there, but that path just failed too.
		if (phone.internet === false) return "phone-offline";
		const longEnough =
			tracker.unansweredStreak >= UNREACHABLE_AFTER_FAILURES &&
			now - tracker.unansweredSince >= UNREACHABLE_AFTER_MS;
		return longEnough ? "unreachable" : "reconnecting";
	}
	const struggling = tracker.recent.filter((o) => o === "slow" || o === "timeout").length;
	return struggling >= SLOW_AFTER ? "slow" : "ok";
}

/**
 * Whether a change in the phone's network is worth racing the endpoints again
 * for: coming back online, or moving between networks (Wi-Fi to cellular).
 * The endpoint that won the last race may belong to the network the phone
 * just left, and nothing else would notice until a poll had failed.
 */
export function networkChangeNeedsRace(
	previous: PhoneNetwork & { type?: string },
	next: PhoneNetwork & { type?: string },
): boolean {
	if (next.connected !== true) return false;
	if (previous.connected === false) return true;
	if (previous.internet === false && next.internet === true) return true;
	// The first report after launch is not a change.
	if (previous.type === undefined) return false;
	return previous.type !== next.type;
}

/**
 * The outcome of a failed poll, from the error it threw. Read structurally so
 * this module stays free of api.ts.
 */
export function failedPollOutcome(error: unknown, status: number | undefined): PollOutcome {
	if (status !== undefined) return "answered-error";
	if (typeof error === "object" && error !== null && "reason" in error && (error as { reason: unknown }).reason === "timeout") {
		return "timeout";
	}
	return "no-answer";
}

export type LinkBannerCopy = { text: string; icon: "wifi-off" | "refresh-cw" | "activity" };

/**
 * One line for the banner above the board, or null where the existing
 * failure copy (or silence) is right.
 */
export function describeLinkHealth(health: LinkHealth, machine: string | undefined): LinkBannerCopy | null {
	const name = machine?.trim() || "your machine";
	switch (health) {
		case "phone-offline":
			return { icon: "wifi-off", text: "You're offline. Check this phone's Wi-Fi or mobile data." };
		case "reconnecting":
			return { icon: "refresh-cw", text: `Reconnecting to ${name}…` };
		case "slow":
			return { icon: "activity", text: "Slow connection. Updates may lag." };
		default:
			return null;
	}
}
