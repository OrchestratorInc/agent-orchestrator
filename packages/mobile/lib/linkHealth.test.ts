import { describe, expect, it } from "vitest";
import { UnreachableError } from "./connectionError";
import {
	classifyLink,
	describeLinkHealth,
	EMPTY_LINK_TRACKER,
	failedPollOutcome,
	networkChangeNeedsRace,
	recordPoll,
	UNKNOWN_PHONE_NETWORK,
	UNREACHABLE_AFTER_FAILURES,
	UNREACHABLE_AFTER_MS,
	type LinkTracker,
	type PollOutcome,
} from "./linkHealth";

const online = { connected: true, internet: true };

function track(outcomes: PollOutcome[], start = 0, step = 8_000): LinkTracker {
	return outcomes.reduce((t, o, i) => recordPoll(t, o, start + i * step), EMPTY_LINK_TRACKER);
}

describe("classifyLink", () => {
	it("is ok while polls answer", () => {
		expect(classifyLink(track(["ok", "ok"]), online, 16_000)).toBe("ok");
	});

	// The bug this exists for: one failed poll used to put "Your machine is
	// offline" on screen, and the next one quietly took it back.
	it("reconnects rather than declaring the machine offline after one failure", () => {
		expect(classifyLink(track(["ok", "no-answer"]), online, 8_000)).toBe("reconnecting");
	});

	it("calls the machine unreachable only after enough failures over enough time", () => {
		const failures: PollOutcome[] = Array(UNREACHABLE_AFTER_FAILURES).fill("no-answer");
		// Enough failures, but all within a couple of seconds (the tunnel's pace).
		const fast = track(failures, 0, 100);
		expect(classifyLink(fast, online, 200)).toBe("reconnecting");
		expect(classifyLink(fast, online, UNREACHABLE_AFTER_MS)).toBe("unreachable");
		// Enough time, but not enough failures.
		const few = track(["timeout", "timeout"], 0, 20_000);
		expect(classifyLink(few, online, 40_000)).toBe("reconnecting");
	});

	it("blames the phone, not the machine, when the phone has no network", () => {
		const t = track(["no-answer", "no-answer", "no-answer", "no-answer"]);
		expect(classifyLink(t, { connected: false, internet: false }, 60_000)).toBe("phone-offline");
		// Even before any poll has failed.
		expect(classifyLink(EMPTY_LINK_TRACKER, { connected: false, internet: undefined }, 0)).toBe("phone-offline");
	});

	it("blames the phone when it is on a network without internet and nothing answers", () => {
		expect(classifyLink(track(["no-answer"]), { connected: true, internet: false }, 0)).toBe("phone-offline");
	});

	// A LAN path works on a Wi-Fi network with no internet, so no-internet alone
	// must not cover up a working board.
	it("stays ok on a network without internet while the machine answers", () => {
		expect(classifyLink(track(["ok"]), { connected: true, internet: false }, 0)).toBe("ok");
	});

	it("treats an unknown phone network as online", () => {
		expect(classifyLink(track(["no-answer"]), UNKNOWN_PHONE_NETWORK, 0)).toBe("reconnecting");
	});

	it("says the connection is slow when answers keep lagging", () => {
		expect(classifyLink(track(["ok", "slow", "ok", "slow", "ok"]), online, 0)).toBe("slow");
		expect(classifyLink(track(["timeout", "ok", "timeout", "ok"]), online, 0)).toBe("slow");
		expect(classifyLink(track(["slow", "ok", "ok", "ok", "ok", "ok"]), online, 0)).toBe("ok");
	});

	// A 401 or 5xx means the machine answered: that is not a network problem,
	// and its own copy (re-scan, check logs) already covers it.
	it("leaves answered errors to their own copy", () => {
		expect(classifyLink(track(["no-answer", "answered-error"]), online, 0)).toBe("ok");
	});
});

describe("recordPoll", () => {
	it("restarts the unanswered run on any answer", () => {
		const t = track(["no-answer", "no-answer", "ok", "timeout"], 0, 1_000);
		expect(t.unansweredStreak).toBe(1);
		expect(t.unansweredSince).toBe(3_000);
	});

	it("keeps only the recent window", () => {
		expect(track(Array(20).fill("ok")).recent).toHaveLength(5);
	});
});

describe("networkChangeNeedsRace", () => {
	it("races when the phone comes back online", () => {
		expect(networkChangeNeedsRace({ connected: false, internet: false, type: "NONE" }, { connected: true, internet: true, type: "WIFI" })).toBe(true);
	});

	it("races when the phone moves between networks", () => {
		expect(networkChangeNeedsRace({ ...online, type: "WIFI" }, { ...online, type: "CELLULAR" })).toBe(true);
	});

	it("races when internet returns on the same network", () => {
		expect(networkChangeNeedsRace({ connected: true, internet: false, type: "WIFI" }, { ...online, type: "WIFI" })).toBe(true);
	});

	it("does not race on the first report, on going offline, or on no change", () => {
		expect(networkChangeNeedsRace({ ...UNKNOWN_PHONE_NETWORK }, { ...online, type: "WIFI" })).toBe(false);
		expect(networkChangeNeedsRace({ ...online, type: "WIFI" }, { connected: false, internet: false, type: "NONE" })).toBe(false);
		expect(networkChangeNeedsRace({ ...online, type: "WIFI" }, { ...online, type: "WIFI" })).toBe(false);
	});
});

describe("failedPollOutcome", () => {
	it("tells timeouts, no answer and answered errors apart", () => {
		expect(failedPollOutcome(new UnreachableError("timeout"), undefined)).toBe("timeout");
		expect(failedPollOutcome(new UnreachableError("offline"), undefined)).toBe("no-answer");
		expect(failedPollOutcome(new TypeError("Network request failed"), undefined)).toBe("no-answer");
		expect(failedPollOutcome(new Error("x"), 503)).toBe("answered-error");
	});
});

describe("describeLinkHealth", () => {
	it("names the machine while reconnecting", () => {
		expect(describeLinkHealth("reconnecting", "studio-mac")?.text).toBe("Reconnecting to studio-mac…");
		expect(describeLinkHealth("reconnecting", undefined)?.text).toBe("Reconnecting to your machine…");
	});

	it("leaves ok and unreachable to the existing copy", () => {
		expect(describeLinkHealth("ok", "x")).toBeNull();
		expect(describeLinkHealth("unreachable", "x")).toBeNull();
	});
});
