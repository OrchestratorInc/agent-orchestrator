import { describe, expect, it } from "vitest";
import { EMPTY_LINK_TRACKER, UNREACHABLE_AFTER_FAILURES, UNREACHABLE_AFTER_MS, type LinkTracker, type PhoneNetwork } from "./linkHealth";
import { errorAfterEdit, NO_CHAT_AGENT_NOTICE, SPAWN_OFFLINE_NOTICE, spawnHostState, spawnNotices, START_DROPPED_NOTICE, type SpawnNoticeInput } from "./spawnNotices";

const base: SpawnNoticeInput = {
	machine: "ok",
	mode: "chat",
	loading: false,
	catalogLoaded: true,
	catalogError: null,
	agentCount: 2,
};

describe("spawnNotices", () => {
	it("shows one line while the desktop is unreachable, whatever else failed", () => {
		expect(spawnNotices({ ...base, machine: "offline", catalogLoaded: false, catalogError: "Couldn't reach your desktop.", agentCount: 0, modelError: "Couldn't reach your desktop." }))
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
		expect(spawnNotices({ ...base, machine: "ok", catalogLoaded: false, catalogError: rescan, agentCount: 0 })).toEqual([rescan]);
	});

	it("collapses the same failure reported by two requests", () => {
		const copy = "Couldn't reach your desktop.";
		expect(spawnNotices({ ...base, catalogError: copy, modelError: copy })).toEqual([copy]);
	});
});

describe("spawnHostState", () => {
	const online: PhoneNetwork = { connected: true, internet: true };
	const now = 1_000_000;
	// Three polls with no answer, over UNREACHABLE_AFTER_MS.
	const givenUp: LinkTracker = { recent: ["no-answer", "no-answer", "no-answer"], unansweredStreak: UNREACHABLE_AFTER_FAILURES, unansweredSince: now - UNREACHABLE_AFTER_MS };
	const failedPoll = { connection: "closed", error: "Couldn't reach your desktop.", errorStatus: null };

	// The store drops the verified connection on purpose whenever the app goes
	// to the background: closed, with no failed poll behind it.
	it("is not offline for a connection closed by an app switch", () => {
		expect(spawnHostState({ connection: "closed", error: null, errorStatus: null, linkTracker: EMPTY_LINK_TRACKER }, online, now)).toBe("ok");
		expect(spawnHostState({ connection: "closed", error: null, errorStatus: null }, online, now)).toBe("ok");
		// Even a failed poll is a blip until the link tracker has given up.
		expect(spawnHostState({ ...failedPoll, linkTracker: { recent: ["no-answer"], unansweredStreak: 1, unansweredSince: now } }, online, now)).toBe("ok");
	});

	it("is offline once the link tracker gives up and the last poll failed as unreachable", () => {
		expect(spawnHostState({ ...failedPoll, linkTracker: givenUp }, online, now)).toBe("offline");
		expect(spawnNotices({ ...base, machine: spawnHostState({ ...failedPoll, linkTracker: givenUp }, online, now) })).toEqual([SPAWN_OFFLINE_NOTICE]);
	});

	// A machine that keeps answering with server errors (a tunnel whose desktop
	// is off answers 502) never counts as unanswered, so the link tracker never
	// gives up on it. New task can't use it: it says so, and its machine choice
	// falls back to another machine or Cloud, as it did before.
	it.each([500, 502, 503])("is offline for a closed connection whose last poll was answered with %s", (status) => {
		const serverError = { ...failedPoll, error: "Your machine had a problem answering.", errorStatus: status };
		expect(spawnHostState(serverError, online, now)).toBe("offline");
		expect(spawnHostState({ ...serverError, linkTracker: givenUp }, online, now)).toBe("offline");
		// Asking again after the app came back is not a failure yet.
		expect(spawnHostState({ ...serverError, connection: "connecting" }, online, now)).toBe("ok");
	});

	it("is ok for an open connection, or no machine", () => {
		expect(spawnHostState({ ...failedPoll, connection: "open", linkTracker: givenUp }, online, now)).toBe("ok");
		expect(spawnHostState(undefined, online, now)).toBe("ok");
	});

	// A refusal stops the poll for good, so a reconnect will not clear it; the
	// notice says what the board says instead of nothing at all.
	it.each([401, 403, 429])("is rejected for a poll the machine refused with %s, link tracker or not", (status) => {
		const refused = { ...failedPoll, error: "Your machine rejected this phone's password.", errorStatus: status };
		expect(spawnHostState(refused, online, now)).toBe("rejected");
		expect(spawnHostState({ ...refused, linkTracker: givenUp }, online, now)).toBe("rejected");
		// Asking again after the app came back is not a refusal yet.
		expect(spawnHostState({ ...refused, connection: "connecting" }, online, now)).toBe("ok");
	});

	it("shows one line in the board's words for a machine that refused this phone", () => {
		const rescan = "Your machine rejected this phone's password. Re-scan the pairing code on that machine.";
		const refused = { ...failedPoll, error: rescan, errorStatus: 401 };
		expect(spawnNotices({ ...base, machine: spawnHostState(refused, online, now), errorStatus: 401, catalogLoaded: false, catalogError: rescan, agentCount: 0 }))
			.toEqual(["Your machine rejected this phone. It was removed from that machine, or the password changed. Scan a new code there."]);
		expect(spawnNotices({ ...base, machine: "rejected", errorStatus: 429 })[0]).toMatch(/^Too many attempts\. /);
	});
});

describe("errorAfterEdit", () => {
	// The notice a dropped Start leaves asks the user to check the details:
	// an edit is that, so it goes.
	it("clears the dropped-Start notice once the form is edited", () => {
		expect(errorAfterEdit(START_DROPPED_NOTICE)).toBeNull();
	});

	// A failed request's error is the server's answer; it stays until the next Start.
	it("keeps any other Start error, and no error stays none", () => {
		expect(errorAfterEdit("Couldn't start the worker. Try again.")).toBe("Couldn't start the worker. Try again.");
		expect(errorAfterEdit(null)).toBeNull();
	});
});
