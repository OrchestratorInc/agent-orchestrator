import { describe, expect, it } from "vitest";
import { createUiUsageTracker, ERROR_REPEAT_MS, type PersistedUiUsage, UI_EVENTS, UI_WINDOW_MS } from "./uiUsage";

const T0 = Date.UTC(2026, 10, 25, 10, 0, 0);

function setup(options: { accept?: (event: string) => boolean } = {}) {
	let now = T0;
	const captures: Array<{ event: string; props: Record<string, unknown> }> = [];
	const saved: PersistedUiUsage[] = [];
	let cleared = 0;
	const tracker = createUiUsageTracker({
		now: () => now,
		capture: (event, props) => {
			if (options.accept && !options.accept(event)) return false;
			captures.push({ event, props });
			return true;
		},
		save: (state) => saved.push(JSON.parse(JSON.stringify(state))),
		clear: () => { cleared += 1; },
	});
	return {
		tracker,
		captures,
		saved,
		cleared: () => cleared,
		advance: (ms: number) => { now += ms; },
		of: (event: string) => captures.filter((c) => c.event === event),
	};
}

describe("surfaces viewed", () => {
	it("reports a surface after a second on screen, once per day", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.advance(500);
		t.tracker.setRoute("prs");
		expect(t.of(UI_EVENTS.viewed)).toEqual([]);
		t.advance(1_500);
		t.tracker.setRoute("board");
		t.advance(2_000);
		t.tracker.setRoute("prs");
		expect(t.of(UI_EVENTS.viewed).map((c) => c.props.surface)).toEqual(["prs", "board"]);
		t.advance(2_000);
		t.tracker.setRoute("board");
		expect(t.of(UI_EVENTS.viewed)).toHaveLength(2);
		t.advance(24 * 3_600_000);
		t.tracker.setRoute("prs");
		expect(t.of(UI_EVENTS.viewed).map((c) => c.props.surface)).toEqual(["prs", "board", "board"]);
	});

	it("tries again when the send was dropped", () => {
		let accept = false;
		const t = setup({ accept: () => accept });
		t.tracker.setRoute("board");
		t.advance(2_000);
		t.tracker.setRoute("prs");
		accept = true;
		t.tracker.setRoute("board");
		t.advance(2_000);
		t.tracker.setRoute("prs");
		expect(t.of(UI_EVENTS.viewed).map((c) => c.props.surface)).toEqual(["board"]);
	});

	it("counts the time before the app goes to the background", () => {
		const t = setup();
		t.tracker.setRoute("settings");
		t.advance(3_000);
		t.tracker.setForeground(false);
		expect(t.of(UI_EVENTS.viewed).map((c) => c.props.surface)).toEqual(["settings"]);
	});
});

describe("usage summary", () => {
	it("sends counts, durations and fixed ids once the window is over", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.advance(60_000);
		t.tracker.setRoute("session");
		t.tracker.setVariant("session", "session_chat");
		t.tracker.sessionOpened("host|s1");
		t.advance(120_000);
		t.tracker.setForeground(false);
		expect(t.of(UI_EVENTS.summary)).toEqual([]);
		t.advance(UI_WINDOW_MS);
		t.tracker.setForeground(true);
		t.advance(10_000);
		t.tracker.setForeground(false);
		const [summary] = t.of(UI_EVENTS.summary);
		expect(summary.props).toMatchObject({
			measurement_schema_version: 1,
			flush_reason: "background",
			active_seconds: 190,
			foreground_count: 2,
			session_open_count: 1,
			session_open_from_board_count: 1,
			surface_board_seconds: 60,
			surface_board_opens: 1,
			surface_session_chat_seconds: 130,
			surface_session_chat_opens: 2,
		});
		// The route's own label was replaced by the chat it showed.
		expect(summary.props).not.toHaveProperty("surface_session_opens");
		expect(Object.values(summary.props).every((v) => typeof v === "number" || v === "background")).toBe(true);
	});

	it("sends a window that ended while the app was closed at the next launch", () => {
		const first = setup();
		first.tracker.setRoute("prs");
		first.advance(30_000);
		first.tracker.setForeground(false);
		const saved = first.saved.at(-1)!;
		const next = setup();
		next.advance(UI_WINDOW_MS + 1);
		next.tracker.restore(saved);
		expect(next.of(UI_EVENTS.summary)[0].props).toMatchObject({ flush_reason: "startup", window_seconds: 30, surface_prs_seconds: 30 });
	});

	it("carries a recent window on at launch", () => {
		const first = setup();
		first.tracker.setRoute("prs");
		first.advance(30_000);
		first.tracker.setForeground(false);
		const next = setup();
		next.advance(60_000);
		next.tracker.restore(first.saved.at(-1)!);
		next.tracker.setRoute("board");
		next.advance(UI_WINDOW_MS);
		next.tracker.setForeground(false);
		expect(next.of(UI_EVENTS.summary)[0].props).toMatchObject({ surface_prs_seconds: 30, foreground_count: 2 });
	});

	// A process that ends without backgrounding keeps its foreground time.
	it("saves foreground time with the surface times, without counting it twice", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.advance(61_000);
		t.tracker.setRoute("prs");
		expect(t.saved.at(-1)!.window.activeMs).toBe(61_000);
		t.advance(UI_WINDOW_MS);
		t.tracker.setForeground(false);
		expect(t.of(UI_EVENTS.summary)[0].props.active_seconds).toBe(Math.round((61_000 + UI_WINDOW_MS) / 1000));
	});

	it("ignores a saved window in an unexpected shape", () => {
		const t = setup();
		t.tracker.restore({ window: { startedAt: "x" }, viewedDay: "", viewed: [] } as unknown as PersistedUiUsage);
		expect(t.captures).toEqual([]);
	});
});

describe("session opens", () => {
	function summaryAfter(t: ReturnType<typeof setup>) {
		t.advance(UI_WINDOW_MS);
		t.tracker.setForeground(false);
		return t.of(UI_EVENTS.summary)[0].props;
	}

	// The app always starts on the board, so that is where these start too.
	it("credits the screen the session was opened from", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setRoute("projects");
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|s1");
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|s2");
		expect(summaryAfter(t)).toMatchObject({ session_open_count: 2, session_open_from_project_count: 1, session_open_from_board_count: 1 });
	});

	it("credits a push, a link or the launch's restore when it marked the opening", () => {
		const t = setup();
		t.tracker.setRoute("board");
		for (const [source, key] of [["notification", "h|a"], ["deep_link", "h|b"], ["restore", "h|c"]] as const) {
			t.tracker.openedBy(source);
			t.tracker.setRoute("session");
			t.tracker.sessionOpened(key);
			t.tracker.setRoute("board");
		}
		expect(summaryAfter(t)).toMatchObject({
			session_open_count: 3,
			session_open_from_notification_count: 1,
			session_open_from_deep_link_count: 1,
			session_open_from_restore_count: 1,
		});
	});

	// A session restored at launch opens from the board the app started on; it
	// is a restore only when RouteRestorer says so, never by default.
	it("does not guess a restore", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|s1");
		const props = summaryAfter(t);
		expect(props).toMatchObject({ session_open_from_board_count: 1 });
		expect(props).not.toHaveProperty("session_open_from_restore_count");
	});

	it("names other when no screen came first", () => {
		const t = setup();
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|s1");
		expect(summaryAfter(t)).toMatchObject({ session_open_from_other_count: 1 });
	});

	it("counts a remount or mode switch once, and a different session again", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|s1");
		t.tracker.sessionOpened("h|s1");
		t.tracker.sessionOpened("h|s2");
		expect(summaryAfter(t)).toMatchObject({ session_open_count: 2, session_open_from_board_count: 1, session_open_from_session_count: 1 });
	});

	// A push for the session already on screen opens nothing new; the next
	// session the person opens is theirs, not the push's.
	it("credits a mark only to the session it names", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|a");
		t.tracker.openedBy("notification", { sessionId: "a" });
		t.tracker.sessionOpened("h|b");
		t.tracker.setRoute("board");
		t.tracker.openedBy("notification", { sessionId: "c" });
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|c");
		expect(summaryAfter(t)).toMatchObject({
			session_open_count: 3,
			session_open_from_board_count: 1,
			session_open_from_session_count: 1,
			session_open_from_notification_count: 1,
		});
	});

	// Session ids repeat across machines: a push names its machine too.
	it("credits a mark only on the machine it names", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.openedBy("notification", { sessionId: "web-1", hostId: "a" });
		t.tracker.setRoute("session");
		// Another machine's session with the same id does not take the mark...
		t.tracker.sessionOpened("b|web-1");
		// ...which is still there for the session it names.
		t.tracker.sessionOpened("a|web-1");
		expect(summaryAfter(t)).toMatchObject({ session_open_count: 2, session_open_from_board_count: 1, session_open_from_notification_count: 1 });
	});

	// A link to a session that takes a while to load is still the link's.
	it("keeps a mark whose session screen came up in time until that session loads", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.openedBy("deep_link", { sessionId: "s1" });
		t.tracker.setRoute("session");
		t.advance(12_000);
		t.tracker.sessionOpened("h|s1");
		expect(summaryAfter(t)).toMatchObject({ session_open_from_deep_link_count: 1 });
	});

	it("drops a waiting mark when the app leaves the session screen without it", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.openedBy("deep_link", { sessionId: "s1" });
		t.tracker.setRoute("session");
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|s1");
		expect(summaryAfter(t)).toMatchObject({ session_open_from_board_count: 1 });
	});

	// Back on a session after a sheet or the files page: it is the one in front again.
	it("treats a session returned to as the current one", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|a");
		t.tracker.setRoute("files");
		t.tracker.setRoute("session");
		t.tracker.sessionReturned("h|a");
		t.tracker.sessionOpened("h|b");
		expect(summaryAfter(t)).toMatchObject({ session_open_count: 2, session_open_from_board_count: 1, session_open_from_session_count: 1 });
	});

	it("does not credit a push that opened nothing", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.openedBy("deep_link");
		t.advance(10_000);
		t.tracker.setRoute("session");
		t.tracker.sessionOpened("h|s1");
		expect(summaryAfter(t)).toMatchObject({ session_open_from_board_count: 1 });
	});
});

describe("errors shown", () => {
	it("sends each error once per five minutes and counts every showing", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.errorShown("screen", "unreachable");
		t.tracker.errorShown("screen", "unreachable");
		t.tracker.errorShown("toast", "action_failed");
		expect(t.of(UI_EVENTS.errorShown).map((c) => c.props)).toEqual([
			{ presentation: "screen", error_kind: "unreachable", surface: "board" },
			{ presentation: "toast", error_kind: "action_failed", surface: "board" },
		]);
		t.advance(ERROR_REPEAT_MS);
		t.tracker.errorShown("screen", "unreachable");
		expect(t.of(UI_EVENTS.errorShown)).toHaveLength(3);
		t.advance(UI_WINDOW_MS);
		t.tracker.setForeground(false);
		expect(t.of(UI_EVENTS.summary)[0].props.error_shown_count).toBe(4);
	});
});

describe("error retries", () => {
	it("sends a dropped error again on its next showing", () => {
		let accept = false;
		const t = setup({ accept: () => accept });
		t.tracker.setRoute("board");
		t.tracker.errorShown("screen", "unreachable");
		accept = true;
		t.tracker.errorShown("screen", "unreachable");
		expect(t.of(UI_EVENTS.errorShown)).toHaveLength(1);
	});
});

describe("opt-out", () => {
	it("drops what was counted, clears the saved copy and stops counting", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.advance(5_000);
		t.tracker.setEnabled(false);
		expect(t.cleared()).toBe(1);
		t.tracker.setRoute("prs");
		t.advance(5_000);
		t.tracker.errorShown("toast", "action_failed");
		t.advance(UI_WINDOW_MS);
		t.tracker.setForeground(false);
		expect(t.captures).toEqual([]);
		t.tracker.setForeground(true);
		t.tracker.setEnabled(true);
		t.tracker.setRoute("board");
		t.advance(UI_WINDOW_MS + 2_000);
		t.tracker.setForeground(false);
		const summary = t.of(UI_EVENTS.summary)[0].props;
		expect(summary).not.toHaveProperty("surface_prs_seconds");
		expect(summary).not.toHaveProperty("error_shown_count");
	});
});

describe("performance", () => {
	function perfAfter(t: ReturnType<typeof setup>) {
		t.advance(UI_WINDOW_MS);
		t.tracker.setForeground(false);
		return t.of(UI_EVENTS.performance)[0]?.props;
	}

	it("times a session from its route coming up to its first opening", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.advance(700);
		t.tracker.sessionOpened("h|s1");
		// A return to the same session is not timed again.
		t.tracker.setRoute("files");
		t.tracker.setRoute("session");
		t.tracker.sessionReturned("h|s1");
		expect(perfAfter(t)).toMatchObject({ session_open_count: 1, session_open_lt1000_count: 1, session_open_p75_ms: 700 });
	});

	// Back on session A after a detour, then straight to B: a switch, not an opening.
	it("does not time a switch to another session after a return", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setRoute("session");
		t.advance(400);
		t.tracker.sessionOpened("h|a");
		t.tracker.setRoute("files");
		t.tracker.setRoute("session");
		t.tracker.sessionReturned("h|a");
		t.advance(5_000);
		t.tracker.sessionOpened("h|b");
		expect(perfAfter(t)).toMatchObject({ session_open_count: 1, session_open_p75_ms: 400 });
	});

	it("times a return to the front until fresh data, and counts one that got none", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.setForeground(false);
		t.advance(60_000);
		t.tracker.setForeground(true);
		t.tracker.resumed();
		const back = T0 + 60_000;
		t.advance(1_800);
		t.tracker.dataRefreshed(back - 5_000); // the refresh from before the return
		t.tracker.dataRefreshed(back + 1_800);
		t.tracker.setForeground(false);
		t.tracker.setForeground(true);
		t.tracker.resumed();
		expect(perfAfter(t)).toMatchObject({ resume_count: 2, resume_fresh_lt3000_count: 1, resume_stale_count: 1 });
	});

	it("sends request and stall counts with the window's own fields", () => {
		const t = setup();
		t.tracker.setRoute("board");
		t.tracker.requestTimed(140, "lan", true);
		t.tracker.requestTimed(null, "tailscale", false);
		t.tracker.stalled(260);
		const props = perfAfter(t);
		expect(props).toMatchObject({ measurement_schema_version: 1, flush_reason: "background", request_count: 1, request_failed_count: 1, request_lan_count: 1, stall_count: 1 });
		expect(props?.window_seconds).toBeGreaterThan(0);
	});

	it("counts nothing while opted out", () => {
		const t = setup();
		t.tracker.setEnabled(false);
		t.tracker.requestTimed(140, "lan", true);
		t.tracker.stalled(900);
		t.tracker.resumed();
		expect(perfAfter(t)).toBeUndefined();
	});

	// A window saved by a build before this one has no perf part.
	it("restores a saved window that has no performance part", () => {
		const t = setup();
		const window = { startedAt: T0 - UI_WINDOW_MS - 1, updatedAt: T0 - 1, activeMs: 30_000, foregrounds: 1, surfaces: { board: { ms: 30_000, opens: 1 } }, sessionOpens: {}, errors: 0 };
		t.tracker.restore({ window, viewedDay: "", viewed: [] } as unknown as PersistedUiUsage);
		expect(t.of(UI_EVENTS.summary)).toHaveLength(1);
		expect(t.of(UI_EVENTS.performance)).toHaveLength(0);
	});
});
