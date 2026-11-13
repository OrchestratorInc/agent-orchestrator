import { describe, expect, it } from "vitest";
import { EMPTY_LINK_TRACKER, recordPoll, UNREACHABLE_AFTER_FAILURES, UNREACHABLE_AFTER_MS, type LinkTracker, type PollOutcome } from "./linkHealth";
import { freshnessByHost, freshnessKey, freshnessLine, machineFreshness, type FreshnessInput } from "./machineFreshness";
import { STALE_AFTER_MS } from "./screenState";

const online = { connected: true, internet: true };
const offline = { connected: false, internet: false };
const NOW = 1_000_000;

function track(outcomes: PollOutcome[], start: number, step = 8_000): LinkTracker {
	return outcomes.reduce((t, o, i) => recordPoll(t, o, start + i * step), EMPTY_LINK_TRACKER);
}

function host(overrides: Partial<FreshnessInput> = {}): FreshnessInput {
	return { connection: "open", loading: false, error: null, lastSyncAt: NOW - 1_000, linkTracker: track(["ok"], NOW - 1_000), ...overrides };
}

describe("machineFreshness", () => {
	it("is live while polls answer", () => {
		expect(machineFreshness(host(), online, NOW)).toBe("live");
	});

	it("is live on a first connection that has never synced", () => {
		expect(machineFreshness(host({ connection: "connecting", loading: true, lastSyncAt: 0, linkTracker: undefined }), online, NOW)).toBe("live");
	});

	it("lags once its rows outlive the stale threshold", () => {
		expect(machineFreshness(host({ lastSyncAt: NOW - STALE_AFTER_MS }), online, NOW)).toBe("lagging");
	});

	it("lags as soon as a poll goes unanswered, before calling it down", () => {
		const tracker = track(["ok", "no-answer"], NOW - 8_000);
		expect(machineFreshness(host({ linkTracker: tracker }), online, NOW)).toBe("lagging");
	});

	// The screenshot this exists for: a failed endpoint race drops the config
	// without setting an error, so the old banner said amber "Showing data from
	// 3m ago" for a machine that had stopped answering.
	it("is unreachable once the link says so, even with no poll error", () => {
		const failures: PollOutcome[] = Array(UNREACHABLE_AFTER_FAILURES).fill("no-answer");
		const tracker = track(failures, NOW - UNREACHABLE_AFTER_MS - 1, UNREACHABLE_AFTER_MS / UNREACHABLE_AFTER_FAILURES);
		expect(machineFreshness(host({ connection: "closed", error: null, lastSyncAt: NOW - 180_000, linkTracker: tracker }), online, NOW)).toBe("unreachable");
	});

	it("is unreachable when the machine answers with an error", () => {
		expect(machineFreshness(host({ connection: "closed", error: "missing or invalid connection password" }), online, NOW)).toBe("unreachable");
	});

	it("never blames the machine for the phone being offline", () => {
		const tracker = track(["ok", "no-answer", "no-answer", "no-answer"], NOW - 60_000);
		expect(machineFreshness(host({ connection: "closed", lastSyncAt: NOW - 600_000, linkTracker: tracker }), offline, NOW)).toBe("live");
	});
});

describe("freshnessKey", () => {
	it("changes only when some machine's state does", () => {
		const a = freshnessByHost([{ hostId: "mac", ...host() }, { hostId: "pc", ...host() }], online, NOW);
		const b = freshnessByHost([{ hostId: "mac", ...host() }, { hostId: "pc", ...host({ lastSyncAt: NOW - 2_000 }) }], online, NOW);
		const c = freshnessByHost([{ hostId: "mac", ...host({ lastSyncAt: NOW - STALE_AFTER_MS }) }], online, NOW);
		expect(freshnessKey(a)).toBe(freshnessKey(b));
		expect(freshnessKey(c)).toBe("mac=lagging\n");
	});
});

describe("freshnessLine", () => {
	const base = { machine: "Mac", lastSyncAt: NOW - 180_000, phoneOffline: false, now: NOW };

	it("says nothing for a live machine", () => {
		expect(freshnessLine({ ...base, freshness: "live" })).toBeNull();
	});

	it("names the machine and the age when it lags", () => {
		expect(freshnessLine({ ...base, freshness: "lagging" })).toEqual({ tone: "warning", text: "Mac · updated 3m ago", retry: true });
	});

	it("says it is reconnecting while the rows are still recent", () => {
		expect(freshnessLine({ ...base, freshness: "lagging", lastSyncAt: NOW - 5_000 })?.text).toBe("Reconnecting to Mac…");
	});

	it("is red when the machine can't be reached", () => {
		expect(freshnessLine({ ...base, freshness: "unreachable" })).toEqual({ tone: "danger", text: "Can't reach Mac · updated 3m ago", retry: true });
	});

	it("leaves the age off when nothing ever synced", () => {
		expect(freshnessLine({ ...base, freshness: "unreachable", lastSyncAt: 0 })?.text).toBe("Can't reach Mac");
	});

	it("puts the phone being offline first, with no retry", () => {
		expect(freshnessLine({ ...base, freshness: "unreachable", phoneOffline: true })).toEqual({ tone: "muted", text: "You're offline · updated 3m ago", retry: false });
	});
});
