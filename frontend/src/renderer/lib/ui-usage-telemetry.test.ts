import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { releaseDailySlot, reserveDailySlot, UiUsageRuntime, type UiUsageRuntimeOptions } from "./ui-usage-telemetry";

type CaptureMock = ReturnType<typeof vi.fn<(event: string, properties: Record<string, unknown>) => Promise<boolean>>>;

const START = Date.UTC(2026, 10, 17, 9, 0, 0);
const settle = async () => {
	for (let i = 0; i < 5; i += 1) await Promise.resolve();
};

describe("UiUsageRuntime", () => {
	let now: number;
	let optedOut: boolean;
	let capture: CaptureMock;
	let runtime: UiUsageRuntime;

	const make = (overrides: UiUsageRuntimeOptions = {}) =>
		new UiUsageRuntime({
			storage: localStorage,
			now: () => now,
			capture,
			optedOut: () => optedOut,
			isWindowActive: () => true,
			afterPaint: (callback) => callback(),
			...overrides,
		});
	const views = () => capture.mock.calls.filter(([event]) => event === "ao.surfaces.viewed");
	const summary = async () => {
		await runtime.flush();
		const call = capture.mock.calls.find(([event]) => event === "ao.ui.usage_summary");
		return call?.[1] as Record<string, unknown> & { surfaces: Array<Record<string, unknown>>; controls: Array<Record<string, unknown>> };
	};
	const surfaceRow = (properties: { surfaces: Array<Record<string, unknown>> }, surface: string) =>
		properties.surfaces.find((row) => row.surface === surface);
	const controlRow = (properties: { controls: Array<Record<string, unknown>> }, control: string) =>
		properties.controls.find((row) => row.control_id === control);
	const click = (target: Element, detail = 1, clientX = 0, clientY = 0) => runtime.click({ target, detail, clientX, clientY });
	const box = (element: Element, rect: { left: number; top: number; width: number; height: number }) => {
		element.getBoundingClientRect = () => ({ ...rect, x: rect.left, y: rect.top, right: rect.left + rect.width, bottom: rect.top + rect.height, toJSON: () => rect });
		Object.defineProperty(element, "clientWidth", { value: rect.width, configurable: true });
		Object.defineProperty(element, "clientHeight", { value: rect.height, configurable: true });
	};

	beforeEach(() => {
		vi.useFakeTimers();
		localStorage.clear();
		now = START;
		optedOut = false;
		capture = vi.fn<(event: string, properties: Record<string, unknown>) => Promise<boolean>>().mockResolvedValue(true);
		document.body.innerHTML = `
			<nav data-telemetry-surface="sidebar" id="sidebar">
				<button data-telemetry-id="sidebar.home" id="home">Home</button>
				<a data-telemetry-id="sidebar.session_row" id="row" href="#">Session</a>
				<div id="blank">Nothing here</div>
			</nav>
			<header data-telemetry-surface="topbar" id="topbar"><span data-telemetry-id="tab.select" id="tab">Tab</span></header>
			<form data-telemetry-surface="composer"><textarea id="input"></textarea></form>`;
		runtime = make();
	});

	afterEach(() => {
		vi.useRealTimers();
	});

	it("times visible surfaces and stops counting after five idle minutes", async () => {
		const token = runtime.surfaceShown("sidebar");
		runtime.surfaceShown("chat");
		now += 10_000;
		runtime.activity();
		runtime.surfaceHidden(token);
		now += 20 * 60_000;
		const properties = await summary();
		expect(surfaceRow(properties, "sidebar")).toMatchObject({ visible_seconds: 10, open_count: 1 });
		// The chat stayed open but only the first five minutes after the last input count.
		expect(surfaceRow(properties, "chat")).toMatchObject({ visible_seconds: 310 });
		expect(properties.active_seconds).toBe(310);
		expect(properties.window_duration_seconds).toBe(1210);
	});

	it("times the focused surface", async () => {
		runtime.focusChanged("composer");
		now += 4_000;
		runtime.focusChanged(undefined);
		now += 4_000;
		expect(surfaceRow(await summary(), "composer")).toMatchObject({ focused_seconds: 4 });
	});

	it("counts one open while the same surface is shown twice", async () => {
		const first = runtime.surfaceShown("terminal");
		const second = runtime.surfaceShown("terminal");
		runtime.surfaceHidden(first);
		runtime.surfaceHidden(second);
		runtime.surfaceHidden(second);
		now += 2_000;
		expect(surfaceRow(await summary(), "terminal")).toMatchObject({ open_count: 1 });
	});

	it("counts a surface that comes back within a second as the same open", async () => {
		const first = runtime.surfaceShown("inspector_summary");
		runtime.surfaceHidden(first);
		now += 200;
		const remount = runtime.surfaceShown("inspector_summary");
		now += 3_000;
		runtime.surfaceHidden(remount);
		now += 2_000;
		runtime.surfaceShown("inspector_summary");
		expect(surfaceRow(await summary(), "inspector_summary")).toMatchObject({ open_count: 2 });
	});

	it("counts menu opens as picks or abandons, including a pick reported just after close", async () => {
		const abandoned = runtime.surfaceShown("cmdk", true);
		runtime.surfaceHidden(abandoned);
		const picked = runtime.surfaceShown("cmdk", true);
		runtime.markPick(picked);
		runtime.surfaceHidden(picked);
		const late = runtime.surfaceShown("cmdk", true);
		runtime.surfaceHidden(late);
		now += 200;
		runtime.markPick(late);
		const tooLate = runtime.surfaceShown("cmdk", true);
		runtime.surfaceHidden(tooLate);
		now += 2_000;
		runtime.markPick(tooLate);
		const properties = await summary();
		expect(properties).toMatchObject({ cmdk_open_count: 4, cmdk_pick_count: 2, cmdk_abandon_count: 2 });
		expect(surfaceRow(properties, "cmdk")).toMatchObject({ open_count: 4, pick_count: 2, abandon_count: 2 });
	});

	it("does not count picks or abandons on surfaces that are not menus", async () => {
		const token = runtime.surfaceShown("board");
		runtime.markPick(token);
		runtime.surfaceHidden(token);
		now += 2_000;
		const row = surfaceRow(await summary(), "board");
		expect(row?.pick_count).toBeUndefined();
		expect(row?.abandon_count).toBeUndefined();
	});

	it("counts pointer clicks and keyboard activations per surface and control", async () => {
		const home = document.getElementById("home")!;
		click(home);
		now += 2_000;
		click(home, 0);
		const properties = await summary();
		expect(properties).toMatchObject({ click_count: 1, keyboard_activation_count: 1 });
		// Clicks are counted per named control and in total, not per surface.
		expect(surfaceRow(properties, "sidebar")).toBeUndefined();
		expect(controlRow(properties, "sidebar.home")).toEqual({ control_id: "sidebar.home", click_count: 1, keyboard_count: 1 });
	});

	it("ignores ids that are not in the registry", async () => {
		document.getElementById("home")!.setAttribute("data-telemetry-id", "sidebar.something_new");
		click(document.getElementById("home")!);
		now += 2_000;
		expect((await summary()).controls).toEqual([]);
	});

	it("counts a click on nothing interactive as a dead click", async () => {
		box(document.getElementById("blank")!, { left: 0, top: 200, width: 300, height: 100 });
		click(document.getElementById("blank")!, 1, 20, 250);
		now += 2_000;
		// A double click selects text and is never dead.
		click(document.getElementById("blank")!, 2, 20, 250);
		now += 2_000;
		click(document.getElementById("home")!, 1, 20, 20);
		const properties = await summary();
		expect(properties.dead_click_count).toBe(1);
		expect(surfaceRow(properties, "sidebar")).toMatchObject({ dead_click_count: 1 });
	});

	it("reports a rage click once per burst and once per surface per day", async () => {
		const home = document.getElementById("home")!;
		for (let i = 0; i < 6; i += 1) {
			click(home);
			now += 100;
		}
		now += 5_000;
		for (let i = 0; i < 4; i += 1) {
			click(home);
			now += 100;
		}
		await settle();
		const properties = await summary();
		expect(properties.rage_click_count).toBe(2);
		expect(controlRow(properties, "sidebar.home")).toMatchObject({ rage_click_count: 2 });
		expect(capture.mock.calls.filter(([event]) => event === "ao.ui.rage_clicked")).toEqual([["ao.ui.rage_clicked", { surface: "sidebar" }]]);
	});

	it("does not call quick double clicks that select text a rage click", async () => {
		const input = document.getElementById("input")!;
		const blank = document.getElementById("blank")!;
		// Two double clicks in a field: four clicks inside a second.
		for (const detail of [1, 2, 1, 2]) {
			click(input, detail);
			now += 100;
		}
		now += 5_000;
		// The same on plain text, which each double click leaves selected.
		for (const detail of [1, 2, 1, 2]) {
			if (detail === 2) window.getSelection()?.selectAllChildren(blank);
			click(blank, detail);
			now += 100;
		}
		window.getSelection()?.removeAllRanges();
		expect((await summary()).rage_click_count).toBeUndefined();
	});

	it("counts a word selected from one of its styled parts as selecting text, not a selection elsewhere", async () => {
		const blank = document.getElementById("blank")!;
		const word = document.createElement("span");
		word.innerHTML = "un<b>der</b>line";
		blank.after(word);
		const part = word.querySelector("b")!;
		const range = document.createRange();
		// The whole word: both ends lie outside the styled part that was clicked.
		range.setStart(word.firstChild!, 0);
		range.setEnd(word.lastChild!, 4);
		for (const detail of [1, 2, 1, 2]) {
			if (detail === 2) {
				window.getSelection()?.removeAllRanges();
				window.getSelection()?.addRange(range);
			}
			click(part, detail);
			now += 100;
		}
		expect(runtime.summaryProperties().rage_click_count).toBeUndefined();
		now += 5_000;
		// A selection left on the word does not hide a burst on other plain text.
		for (const detail of [1, 2, 3, 4]) {
			click(blank, detail);
			now += 100;
		}
		window.getSelection()?.removeAllRanges();
		word.remove();
		expect((await summary()).rage_click_count).toBe(1);
	});

	it("still calls a fast burst on a button a rage click, though the browser numbers it as a multi-click", async () => {
		const home = document.getElementById("home")!;
		for (const detail of [1, 2, 3, 4]) {
			click(home, detail);
			now += 100;
		}
		expect((await summary()).rage_click_count).toBe(1);
	});

	it("does not call a click that closes a menu, or that the app cancels, a dead click", async () => {
		const blank = document.getElementById("blank")!;
		box(blank, { left: 0, top: 200, width: 300, height: 100 });
		// The press landed outside an open menu, so the click only closed it.
		runtime.pointerDown(true);
		click(blank, 1, 20, 250);
		now += 2_000;
		// A listener after this one handled the click and prevented its default.
		runtime.click({ target: blank, detail: 1, clientX: 20, clientY: 250, defaultPrevented: true });
		now += 2_000;
		// With no menu open, the same click is dead.
		runtime.pointerDown(false);
		click(blank, 1, 20, 250);
		expect((await summary()).dead_click_count).toBe(1);
	});

	it("does not call a click on a text cursor a dead click", async () => {
		const blank = document.getElementById("blank")!;
		box(blank, { left: 0, top: 200, width: 300, height: 100 });
		// A place that focuses a field when clicked, such as the composer's padding.
		blank.style.cursor = "text";
		click(blank, 1, 20, 250);
		expect((await summary()).dead_click_count).toBeUndefined();
	});

	it("does not call slow, spread-out clicks a rage click", async () => {
		const home = document.getElementById("home")!;
		for (let i = 0; i < 6; i += 1) {
			click(home);
			now += 400;
		}
		expect((await summary()).rage_click_count).toBeUndefined();
	});

	it("tells hover-then-click from hover-only on hover controls", async () => {
		const row = document.getElementById("row")!;
		runtime.pointerOver(row);
		now += 600;
		click(row);
		runtime.pointerOver(document.getElementById("blank"));
		runtime.pointerOver(document.getElementById("tab"));
		now += 700;
		runtime.pointerOver(document.getElementById("blank"));
		// Too short to count either way.
		runtime.pointerOver(document.getElementById("tab"));
		now += 100;
		runtime.endHover();
		const properties = await summary();
		expect(properties).toMatchObject({ hover_click_count: 1, hover_only_count: 1 });
		expect(controlRow(properties, "sidebar.session_row")).toMatchObject({ hover_click_count: 1 });
		expect(controlRow(properties, "tab.select")).toMatchObject({ hover_only_count: 1 });
	});

	it("counts a hover-then-click only on the element that was hovered", async () => {
		const other = document.createElement("a");
		other.setAttribute("data-telemetry-id", "sidebar.session_row");
		document.getElementById("sidebar")!.append(other);
		runtime.pointerOver(document.getElementById("row"));
		now += 600;
		click(other);
		expect((await summary()).hover_click_count).toBeUndefined();
	});

	it("does not track hovers while analytics is off", async () => {
		optedOut = true;
		runtime.pointerOver(document.getElementById("row"));
		optedOut = false;
		now += 600;
		click(document.getElementById("row")!);
		expect((await summary()).hover_click_count).toBeUndefined();
	});

	it("attributes session switches to the input that started them", async () => {
		runtime.sessionViewed("host|a");
		click(document.getElementById("row")!);
		runtime.sessionViewed("host|b");
		runtime.sessionViewed("host|b");
		now += 5_000;
		click(document.getElementById("tab")!);
		runtime.sessionViewed("host|c");
		now += 5_000;
		runtime.noteIntent("cmdk");
		runtime.sessionViewed("host|d");
		now += 5_000;
		runtime.sessionViewed("host|e");
		expect(await summary()).toMatchObject({
			session_switch_count: 4,
			switch_from_sidebar_count: 1,
			switch_from_tab_count: 1,
			switch_from_cmdk_count: 1,
			switch_from_other_count: 1,
		});
	});

	it("does not count a key chord on the page as a shortcut", async () => {
		const input = document.getElementById("input")!;
		// App shortcuts are claimed by main and reported by id; a chord the page sees is editing or the OS.
		runtime.keyDown({ key: "Backspace", metaKey: true, ctrlKey: false, altKey: false, target: input, repeat: false });
		runtime.keyDown({ key: "k", metaKey: true, ctrlKey: false, altKey: false, target: document.body, repeat: false });
		now += 2_000;
		expect((await summary()).shortcut_count).toBeUndefined();
	});

	it("counts each app shortcut by id, on its control too, and by Cmd+K apart from its keys", async () => {
		runtime.shortcutUsed("toggle-inspector", "key");
		runtime.shortcutUsed("toggle-inspector", "key");
		runtime.shortcutUsed("toggle-inspector", "cmdk");
		runtime.shortcutUsed("find-in-chat", "key");
		// Not a shortcut AO has: dropped.
		runtime.shortcutUsed("format-disk", "key");
		const properties = (await summary()) as Awaited<ReturnType<typeof summary>> & { shortcuts: Array<Record<string, unknown>> };
		expect(properties.shortcut_count).toBe(3);
		expect(properties.shortcuts).toEqual([
			{ shortcut_id: "toggle-inspector", key_count: 2, cmdk_count: 1 },
			{ shortcut_id: "find-in-chat", key_count: 1 },
		]);
		// Its chord shows beside the button's clicks; Cmd+K does not.
		expect(controlRow(properties, "topbar.inspector")).toEqual({ control_id: "topbar.inspector", shortcut_count: 2 });
		expect(properties.measurement_schema_version).toBe(2);
	});

	it("names a shortcut as what switched the session", async () => {
		runtime.sessionViewed("host|a");
		runtime.shortcutUsed("next-session", "key");
		runtime.sessionViewed("host|b");
		const properties = await summary();
		expect(properties.switch_from_shortcut_count).toBe(1);
		expect(controlRow(properties, "sidebar.session_row")).toMatchObject({ shortcut_count: 1 });
	});

	it("keeps shortcut counts saved before a restart, and drops unknown ones", async () => {
		runtime.shortcutUsed("toggle-sidebar", "key");
		runtime.checkpoint();
		const saved = JSON.parse(localStorage.getItem("ao.telemetry.uiUsage.v1")!) as { shortcuts: Record<string, unknown> };
		saved.shortcuts["format-disk"] = { key_count: 9 };
		localStorage.setItem("ao.telemetry.uiUsage.v1", JSON.stringify(saved));
		runtime = make();
		const properties = (await summary()) as Awaited<ReturnType<typeof summary>> & { shortcuts: Array<Record<string, unknown>> };
		expect(properties.shortcuts).toEqual([{ shortcut_id: "toggle-sidebar", key_count: 1 }]);
	});

	it("counts sends, attachments, discarded drafts, model changes and errors", async () => {
		runtime.messageSent("keyboard", "steer", true);
		runtime.messageSent("button", "queue", false);
		runtime.messageSent("button", "send", false);
		runtime.attachmentAdded("paste");
		runtime.attachmentAdded("drop");
		runtime.draftDiscarded();
		runtime.modelChanged();
		click(document.getElementById("home")!);
		runtime.errorShown("toast", "toast");
		runtime.errorShown("chat_turn_failed", "inline", "chat", "AUTH_REQUIRED");
		const properties = await summary();
		expect(properties).toMatchObject({
			send_count: 3,
			send_keyboard_count: 1,
			send_button_count: 2,
			steer_count: 1,
			queue_count: 1,
			send_with_attachment_count: 1,
			attachment_add_paste_count: 1,
			attachment_add_drop_count: 1,
			composer_draft_discarded_count: 1,
			model_picker_change_count: 1,
			error_shown_count: 2,
			error_toast_count: 1,
		});
		// The toast is attributed to the surface clicked just before it.
		expect(surfaceRow(properties, "sidebar")).toMatchObject({ error_count: 1 });
		expect(surfaceRow(properties, "chat")).toMatchObject({ error_count: 1 });
		expect(capture).toHaveBeenCalledWith("ao.ui.error_shown", { presentation: "toast", error_kind: "toast", surface: "sidebar" });
		expect(capture).toHaveBeenCalledWith("ao.ui.error_shown", {
			presentation: "inline",
			error_kind: "chat_turn_failed",
			surface: "chat",
			error_code: "AUTH_REQUIRED",
		});
	});

	it("counts every repeat of an error but sends it once per five minutes", async () => {
		for (let i = 0; i < 3; i += 1) {
			runtime.errorShown("daemon_unavailable", "banner", undefined, "EXITED");
			await settle();
			now += 10_000;
		}
		runtime.errorShown("daemon_unavailable", "banner", undefined, "NOT_READY");
		await settle();
		now += 5 * 60_000;
		runtime.errorShown("daemon_unavailable", "banner", undefined, "EXITED");
		const sent = capture.mock.calls.filter(([event]) => event === "ao.ui.error_shown").map(([, properties]) => properties.error_code);
		expect(sent).toEqual(["EXITED", "NOT_READY", "EXITED"]);
		expect((await summary()).error_shown_count).toBe(5);
	});

	it("sends an error again when the first send did not go out", async () => {
		capture.mockResolvedValueOnce(false);
		runtime.errorShown("daemon_unavailable", "banner", undefined, "EXITED");
		// Still in flight: a repeat waits for it instead of sending twice.
		runtime.errorShown("daemon_unavailable", "banner", undefined, "EXITED");
		await settle();
		now += 10_000;
		runtime.errorShown("daemon_unavailable", "banner", undefined, "EXITED");
		await settle();
		expect(capture.mock.calls.filter(([event]) => event === "ao.ui.error_shown")).toHaveLength(2);
	});

	it("keys error repeats on the code actually sent, so unknown codes share one", async () => {
		runtime.errorShown("daemon_unavailable", "banner", undefined, "CUSTOMER_ONE");
		await settle();
		runtime.errorShown("daemon_unavailable", "banner", undefined, "CUSTOMER_TWO");
		await settle();
		const sent = capture.mock.calls.filter(([event]) => event === "ao.ui.error_shown");
		expect(sent).toEqual([["ao.ui.error_shown", { presentation: "banner", error_kind: "daemon_unavailable" }]]);
	});

	it("sends a surface view once per day, after it stayed on screen for a second", async () => {
		const brief = runtime.surfaceShown("board");
		vi.advanceTimersByTime(500);
		runtime.surfaceHidden(brief);
		vi.advanceTimersByTime(1_000);
		expect(capture.mock.calls.filter(([event]) => event === "ao.surfaces.viewed")).toEqual([]);

		const restored = runtime.surfaceShown("sidebar");
		vi.advanceTimersByTime(1_000);
		runtime.surfaceHidden(restored);
		runtime.surfaceShown("sidebar");
		vi.advanceTimersByTime(1_000);
		await settle();
		expect(capture.mock.calls.filter(([event]) => event === "ao.surfaces.viewed")).toEqual([
			["ao.surfaces.viewed", { surface: "sidebar", opened_by: "restore" }],
		]);

		// A new runtime the same day (an app restart) does not send it again.
		runtime = make();
		runtime.surfaceShown("sidebar");
		vi.advanceTimersByTime(1_000);
		await settle();
		expect(capture.mock.calls.filter(([event]) => event === "ao.surfaces.viewed")).toHaveLength(1);
	});

	it("frees the daily view slot when the send fails", async () => {
		capture.mockResolvedValueOnce(false);
		const first = runtime.surfaceShown("board");
		vi.advanceTimersByTime(1_000);
		await settle();
		runtime.surfaceHidden(first);
		runtime.surfaceShown("board");
		vi.advanceTimersByTime(1_000);
		await settle();
		expect(capture.mock.calls.filter(([event]) => event === "ao.surfaces.viewed")).toHaveLength(2);
	});

	it("counts a deliberate open as a feature's first use, but not a restored one", async () => {
		runtime.surfaceShown("board");
		vi.advanceTimersByTime(1_000);
		now += 30_000;
		runtime.noteIntent("cmdk");
		runtime.surfaceShown("archive_modal");
		vi.advanceTimersByTime(1_000);
		await settle();
		const firstUses = capture.mock.calls.filter(([event]) => event === "ao.features.first_used");
		expect(firstUses).toEqual([["ao.features.first_used", { feature: "archive_modal" }]]);
		expect(capture).toHaveBeenCalledWith("ao.surfaces.viewed", { surface: "archive_modal", opened_by: "cmdk" });
	});

	it("marks a feature as used only after its event was sent", async () => {
		capture.mockResolvedValueOnce(false);
		runtime.featureUsed("steer");
		await settle();
		runtime.featureUsed("steer");
		await settle();
		runtime.featureUsed("steer");
		await settle();
		expect(capture.mock.calls.filter(([event]) => event === "ao.features.first_used")).toHaveLength(2);
		// Persisted across restarts.
		runtime = make();
		runtime.featureUsed("steer");
		await settle();
		expect(capture.mock.calls.filter(([event]) => event === "ao.features.first_used")).toHaveLength(2);
	});

	it("skips windows with nothing counted and starts a new window after each flush", async () => {
		await runtime.flush();
		expect(capture).not.toHaveBeenCalled();

		click(document.getElementById("home")!);
		const first = await summary();
		capture.mockClear();
		click(document.getElementById("home")!);
		const second = await summary();
		expect(first.window_id).toEqual(expect.stringMatching(/^[0-9a-f-]{36}$/));
		expect(second.window_id).not.toBe(first.window_id);
		expect(second.click_count).toBe(1);
	});

	it("resumes a persisted window after a restart", async () => {
		click(document.getElementById("home")!);
		runtime.checkpoint();
		runtime = make();
		click(document.getElementById("home")!);
		expect((await summary()).click_count).toBe(2);
	});

	it("keeps and sends nothing while analytics is off, and drops what was counted", async () => {
		click(document.getElementById("home")!);
		runtime.checkpoint();
		expect(localStorage.getItem("ao.telemetry.uiUsage.v1")).not.toBeNull();

		optedOut = true;
		runtime.checkpoint();
		expect(localStorage.getItem("ao.telemetry.uiUsage.v1")).toBeNull();
		click(document.getElementById("home")!);
		runtime.messageSent("keyboard", "send", false);
		runtime.featureUsed("cmdk");
		runtime.surfaceShown("sidebar");
		now += 60_000;
		vi.advanceTimersByTime(1_000);
		await runtime.flush();
		await settle();
		expect(capture).not.toHaveBeenCalled();

		optedOut = false;
		click(document.getElementById("home")!);
		const properties = await summary();
		expect(properties.click_count).toBe(1);
		expect(properties.send_count).toBeUndefined();
		// The minute on screen while analytics was off is not counted after it comes back on.
		expect(properties.active_seconds).toBe(0);
		expect(surfaceRow(properties, "sidebar")?.visible_seconds).toBeUndefined();
	});

	it("waits out a double click before calling its first click dead", async () => {
		runtime = make({ afterPaint: (callback) => setTimeout(callback, 500) });
		const blank = document.getElementById("blank")!;
		box(blank, { left: 0, top: 200, width: 300, height: 100 });
		click(blank, 1, 20, 250);
		click(blank, 2, 20, 250);
		vi.advanceTimersByTime(500);
		now += 2_000;
		// An isolated click still counts, once.
		click(blank, 1, 20, 250);
		vi.advanceTimersByTime(500);
		expect((await summary()).dead_click_count).toBe(1);
	});

	it("gives each click its own settle time before calling it dead", async () => {
		runtime = make({ afterPaint: (callback) => setTimeout(callback, 500) });
		const blank = document.getElementById("blank")!;
		const other = document.createElement("div");
		document.getElementById("sidebar")!.append(other);
		box(blank, { left: 0, top: 200, width: 300, height: 100 });
		box(other, { left: 0, top: 300, width: 300, height: 100 });
		click(blank, 1, 20, 250);
		vi.advanceTimersByTime(450);
		click(other, 1, 20, 350);
		// The first click is judged; the second is still settling.
		vi.advanceTimersByTime(50);
		vi.advanceTimersByTime(150);
		// Its double click arrives 200 ms in and cancels it.
		click(other, 2, 20, 350);
		vi.advanceTimersByTime(500);
		expect((await summary()).dead_click_count).toBe(1);
	});

	it("waits for the window to be on screen before sending a view", async () => {
		let active = true;
		runtime = make({ isWindowActive: () => active });
		runtime.surfaceShown("board");
		vi.advanceTimersByTime(500);
		active = false;
		runtime.windowStateChanged();
		vi.advanceTimersByTime(5_000);
		await settle();
		expect(views()).toEqual([]);
		active = true;
		runtime.windowStateChanged();
		vi.advanceTimersByTime(1_000);
		await settle();
		expect(views()).toHaveLength(1);
	});

	it("treats a capture that throws as not sent, so the daily slot is freed", async () => {
		capture.mockRejectedValueOnce(new Error("offline"));
		const first = runtime.surfaceShown("board");
		vi.advanceTimersByTime(1_000);
		await settle();
		runtime.surfaceHidden(first);
		runtime.surfaceShown("board");
		vi.advanceTimersByTime(1_000);
		await settle();
		expect(views()).toHaveLength(2);
	});

	it("drops damaged rows from a saved window and still sends the rest", async () => {
		localStorage.setItem("ao.telemetry.uiUsage.v1", JSON.stringify({
			version: 1,
			windowId: "123e4567-e89b-42d3-a456-426614174000",
			windowStartedAt: START - 3_600_000,
			windowFlushAt: START - 1,
			activeMs: 0,
			counters: { send_count: 2, bogus_count: 5, steer_count: "x" },
			surfaces: { board: null, sidebar: { visible_ms: 5_000, open_count: -1 }, secret_panel: { open_count: 1 } },
			controls: { "composer.send": { click_count: 3 }, "button.secret": { click_count: 1 } },
		}));
		runtime = make();
		expect(runtime.isFlushOverdue()).toBe(true);
		await runtime.flush("startup");
		const properties = capture.mock.calls.find(([event]) => event === "ao.ui.usage_summary")?.[1];
		expect(properties).toMatchObject({
			send_count: 2,
			surfaces: [{ surface: "sidebar", visible_seconds: 5 }],
			controls: [{ control_id: "composer.send", click_count: 3 }],
		});
		expect(properties).not.toHaveProperty("bogus_count");
		expect(properties).not.toHaveProperty("steer_count");
	});

	it("counts no time from while analytics was off once it is back on", async () => {
		click(document.getElementById("home")!);
		optedOut = true;
		now += 30_000;
		optedOut = false;
		// The app discards the window on every change of the setting, opt-in included.
		runtime.discard();
		click(document.getElementById("home")!);
		now += 1_000;
		expect((await summary()).active_seconds).toBe(1);
	});

	it("lets flush listeners see each window", async () => {
		const listener = vi.fn();
		runtime.onFlushed(listener);
		click(document.getElementById("home")!);
		await runtime.flush("startup");
		expect(listener).toHaveBeenCalledWith({ windowId: expect.any(String), reason: "startup" });
		expect(capture.mock.calls[0][1]).toMatchObject({ flush_reason: "startup" });
	});
});

describe("daily slots", () => {
	beforeEach(() => localStorage.clear());

	it("reserves each slot once per UTC day", () => {
		expect(reserveDailySlot(localStorage, "k", "sidebar", START)).toBe(true);
		expect(reserveDailySlot(localStorage, "k", "sidebar", START + 60_000)).toBe(false);
		expect(reserveDailySlot(localStorage, "k", "board", START)).toBe(true);
		expect(reserveDailySlot(localStorage, "k", "sidebar", START + 24 * 3_600_000)).toBe(true);
	});

	it("releases a slot for a send that failed", () => {
		reserveDailySlot(localStorage, "k", "sidebar", START);
		releaseDailySlot(localStorage, "k", "sidebar", START);
		expect(reserveDailySlot(localStorage, "k", "sidebar", START)).toBe(true);
	});

	it("treats unreadable state as empty", () => {
		localStorage.setItem("k", "{not json");
		expect(reserveDailySlot(localStorage, "k", "sidebar", START)).toBe(true);
		expect(reserveDailySlot(undefined, "k", "sidebar", START)).toBe(false);
	});
});
