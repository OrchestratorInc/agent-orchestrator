import { describe, expect, it } from "vitest";
import { EMPTY_LINK_TRACKER, UNREACHABLE_AFTER_FAILURES, UNREACHABLE_AFTER_MS, type LinkTracker, type PhoneNetwork } from "./linkHealth";
import { NO_CHAT_AGENT_NOTICE, SPAWN_OFFLINE_NOTICE, spawnHostOffline, spawnNotices, type SpawnNoticeInput } from "./spawnNotices";

const base: SpawnNoticeInput = {
	offline: false,
	mode: "chat",
	loading: false,
	catalogLoaded: true,
	catalogError: null,
	agentCount: 2,
};

describe("spawnNotices", () => {
	it("shows one line while the desktop is unreachable, whatever else failed", () => {
		expect(spawnNotices({ ...base, offline: true, catalogLoaded: false, catalogError: "Couldn't reach your desktop.", agentCount: 0, modelError: "Couldn't reach your desktop." }))
			.toEqual([SPAWN_OFFLINE_NOTICE]);
	});

	it("does not blame the install when the catalog failed to load", () => {
		const notices = spawnNotices({ ...base, catalogLoaded: false, catalogError: "Couldn't reach your desktop.", agentCount: 0 });
		expect(notices).not.toContain(NO_CHAT_AGENT_NOTICE);
		expect(notices).toEqual(["Couldn't reach your desktop."]);
	});

	it("says no agent supports Chat only when the catalog answered empty", () => {
		expect(spawnNotices({ ...base, agentCount: 0 })).toEqual([NO_CHAT_AGENT_NOTICE]);
		expect(spawnNotices({ ...base, agentCount: 0, mode: "tui" })).toEqual([]);
		expect(spawnNotices({ ...base, agentCount: 0, loading: true })).toEqual([]);
	});

	it("lets a rejected password through, since no reconnect will fix it", () => {
		const rescan = "Your desktop rejected this phone's password. Re-scan the pairing code on your desktop.";
		expect(spawnNotices({ ...base, offline: false, catalogLoaded: false, catalogError: rescan, agentCount: 0 })).toEqual([rescan]);
	});

	it("collapses the same failure reported by two requests", () => {
		const copy = "Couldn't reach your desktop.";
		expect(spawnNotices({ ...base, catalogError: copy, modelError: copy })).toEqual([copy]);
	});
});

describe("spawnHostOffline", () => {
	const online: PhoneNetwork = { connected: true, internet: true };
	const now = 1_000_000;
	// Three polls with no answer, over UNREACHABLE_AFTER_MS.
	const givenUp: LinkTracker = { recent: ["no-answer", "no-answer", "no-answer"], unansweredStreak: UNREACHABLE_AFTER_FAILURES, unansweredSince: now - UNREACHABLE_AFTER_MS };
	const failedPoll = { connection: "closed", error: "Couldn't reach your desktop.", errorStatus: null };

	// The store drops the verified connection on purpose whenever the app goes
	// to the background: closed, with no failed poll behind it.
	it("is not offline for a connection closed by an app switch", () => {
		expect(spawnHostOffline({ connection: "closed", error: null, errorStatus: null, linkTracker: EMPTY_LINK_TRACKER }, online, now)).toBe(false);
		expect(spawnHostOffline({ connection: "closed", error: null, errorStatus: null }, online, now)).toBe(false);
		// Even a failed poll is a blip until the link tracker has given up.
		expect(spawnHostOffline({ ...failedPoll, linkTracker: { recent: ["no-answer"], unansweredStreak: 1, unansweredSince: now } }, online, now)).toBe(false);
	});

	it("is offline once the link tracker gives up and the last poll failed as unreachable", () => {
		expect(spawnHostOffline({ ...failedPoll, linkTracker: givenUp }, online, now)).toBe(true);
		expect(spawnNotices({ ...base, offline: spawnHostOffline({ ...failedPoll, linkTracker: givenUp }, online, now) })).toEqual([SPAWN_OFFLINE_NOTICE]);
	});

	it("is not offline for a rejected password, an open connection, or no machine", () => {
		expect(spawnHostOffline({ ...failedPoll, errorStatus: 401, linkTracker: givenUp }, online, now)).toBe(false);
		expect(spawnHostOffline({ ...failedPoll, connection: "open", linkTracker: givenUp }, online, now)).toBe(false);
		expect(spawnHostOffline(undefined, online, now)).toBe(false);
	});
});
