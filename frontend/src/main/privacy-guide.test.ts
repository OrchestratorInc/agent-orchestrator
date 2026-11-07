import { describe, expect, it, vi } from "vitest";
import {
	coercePrivacyGuideRequest,
	parsePrivacyGuideAction,
	placePrivacyGuide,
	PRIVACY_GUIDE_HEIGHT,
	PRIVACY_PANE_URLS,
	PrivacyGuide,
	renderPrivacyGuideHtml,
	type PrivacyGuideRequest,
	type PrivacyGuideWindow,
} from "./privacy-guide";
import type { SettingsWindowSnapshot } from "./settings-window-watch";

const request: PrivacyGuideRequest = {
	pane: "accessibility",
	granted: { accessibility: false, screenRecording: false },
	appearance: { scheme: "dark", colors: { bg: "oklch(0.3 0.002 250)", fg: "#fafafa", muted: "rgb(180 180 180)", border: "oklch(1 0 0 / 7%)" } },
	text: {
		drag: "Drag CuaDriver into the list above",
		hint: "Already in the list? Turn on its switch.",
		allowed: "CuaDriver is allowed here",
		allowedHint: "Turn off its switch to revoke access.",
		labels: { accessibility: "Accessibility", screenRecording: "Screen Recording" },
		granted: "Granted",
		close: "Close",
	},
};

const workArea = { x: 0, y: 25, width: 1920, height: 1055 };
const settingsWindow = { x: 590, y: 213, width: 740, height: 625 };
const SETTINGS_PID = 500;
const OWN_PID = 42;

function fakeWindow() {
	let navigate: (url: string) => void = () => undefined;
	let closed: () => void = () => undefined;
	let destroyed = false;
	let visible = false;
	const window = {
		loadURL: vi.fn().mockResolvedValue(undefined),
		setBounds: vi.fn(),
		showInactive: vi.fn(() => { visible = true; }),
		hide: vi.fn(() => { visible = false; }),
		isVisible: () => visible,
		isFocused: () => false,
		close: vi.fn(() => {
			destroyed = true;
			closed();
		}),
		isDestroyed: () => destroyed,
		onNavigate: (handler: (url: string) => void) => { navigate = handler; },
		onClosed: (handler: () => void) => { closed = handler; },
		navigate: (url: string) => navigate(url),
		html: () => decodeURIComponent(String(window.loadURL.mock.lastCall?.[0]).replace(/^data:text\/html;charset=utf-8,/, "")),
	} satisfies PrivacyGuideWindow & Record<string, unknown>;
	return window;
}

function guide() {
	const window = fakeWindow();
	let emit: (snapshot: SettingsWindowSnapshot) => void = () => undefined;
	let fail: () => void = () => undefined;
	const stop = vi.fn();
	const deps = {
		createWindow: vi.fn(() => window),
		openExternal: vi.fn().mockResolvedValue(undefined),
		readIcon: vi.fn().mockResolvedValue("data:image/png;base64,AAAA"),
		watchSettings: vi.fn((onSnapshot: typeof emit, onError: () => void) => {
			emit = onSnapshot;
			fail = onError;
			return stop;
		}),
		workAreaFor: () => workArea,
		fallbackBounds: () => ({ x: 660, y: 984, width: 600, height: PRIVACY_GUIDE_HEIGHT }),
		ownPid: OWN_PID,
	};
	return { guide: new PrivacyGuide(deps), deps, window, stop, emit: (s: SettingsWindowSnapshot) => emit(s), fail: () => fail() };
}

const front = (bounds = settingsWindow): SettingsWindowSnapshot => ({ frontPid: SETTINGS_PID, settingsPid: SETTINGS_PID, bounds });

describe("coercePrivacyGuideRequest", () => {
	it("accepts a complete request", () => {
		expect(coercePrivacyGuideRequest(request)).toEqual(request);
	});

	it("refuses unknown panes and missing copy", () => {
		expect(coercePrivacyGuideRequest({ ...request, pane: "camera" })).toBeUndefined();
		expect(coercePrivacyGuideRequest({ ...request, text: { ...request.text, hint: "" } })).toBeUndefined();
		expect(coercePrivacyGuideRequest({ ...request, text: { ...request.text, drag: { accessibility: "x" } } })).toBeUndefined();
		expect(coercePrivacyGuideRequest({ ...request, text: { ...request.text, allowed: undefined } })).toBeUndefined();
		expect(coercePrivacyGuideRequest(null)).toBeUndefined();
	});

	it("keeps only plain colors and falls back to dark for an unknown scheme", () => {
		const coerced = coercePrivacyGuideRequest({
			...request,
			appearance: { scheme: "sepia", colors: { bg: "red; } body { background: url(x)", fg: "</style><script>", muted: "color-mix(in oklch, #fff 40%, oklch(0.3 0 0))", border: 7 } },
		});
		expect(coerced?.appearance).toEqual({ scheme: "dark", colors: { muted: "color-mix(in oklch, #fff 40%, oklch(0.3 0 0))" } });
		expect(coercePrivacyGuideRequest({ ...request, appearance: undefined })?.appearance).toEqual({ scheme: "dark", colors: {} });
	});

	it("accepts only named color functions, nested ones included", () => {
		const colorsOf = (colors: Record<string, string>) => coercePrivacyGuideRequest({ ...request, appearance: { scheme: "dark", colors } })?.appearance.colors;
		// What AO's themes produce: oklch() and color-mix() in the default theme, hex in the catalog.
		expect(colorsOf({ bg: "oklch(0.3 0.002 250)", fg: "#1e1e2e", muted: "rgb(180 180 180)", border: "#ffffff17" }))
			.toEqual({ bg: "oklch(0.3 0.002 250)", fg: "#1e1e2e", muted: "rgb(180 180 180)", border: "#ffffff17" });
		expect(colorsOf({ bg: "url(x)", fg: "var(--x)", muted: "color-mix(in oklch, url(x) 50%, red)", border: "image-set(x)" })).toEqual({});
	});
});

describe("parsePrivacyGuideAction", () => {
	it("reads the panel's own links only", () => {
		expect(parsePrivacyGuideAction("ao-privacy-guide://pane/screenRecording")).toEqual({ kind: "pane", pane: "screenRecording" });
		expect(parsePrivacyGuideAction("ao-privacy-guide://close")).toEqual({ kind: "close" });
		expect(parsePrivacyGuideAction("ao-privacy-guide://pane/camera")).toBeUndefined();
		expect(parsePrivacyGuideAction("https://example.com/pane/accessibility")).toBeUndefined();
	});
});

describe("placePrivacyGuide", () => {
	it("docks under the System Settings window at its width", () => {
		expect(placePrivacyGuide(settingsWindow, workArea)).toEqual({ x: 590, y: 846, width: 740, height: PRIVACY_GUIDE_HEIGHT });
	});

	it("overlaps the window's bottom edge when there is no room below", () => {
		const low = { ...settingsWindow, y: 440 };
		expect(placePrivacyGuide(low, workArea).y).toBe(440 + 625 - PRIVACY_GUIDE_HEIGHT - 8);
	});

	it("keeps a narrow window's strip readable and on screen", () => {
		const placed = placePrivacyGuide({ x: 1700, y: 100, width: 300, height: 400 }, workArea);
		expect(placed.width).toBe(560);
		expect(placed.x + placed.width).toBeLessThanOrEqual(workArea.width - 8);
	});
});

describe("renderPrivacyGuideHtml", () => {
	it("offers a draggable CuaDriver tile and marks the chosen permission", () => {
		const html = renderPrivacyGuideHtml(request, { kind: "image", dataUrl: "data:image/png;base64,AAAA" });
		expect(html).toContain('class="app draggable" draggable="true" data-drag-app');
		expect(html).toContain('src="data:image/png;base64,AAAA"');
		expect(html).toContain("Drag CuaDriver into the list above");
		expect(html).toContain('class="choice selected" href="ao-privacy-guide://pane/accessibility" role="tab" aria-selected="true"');
	});

	it("escapes translated copy, ticks granted panes, and drops non-image icons", () => {
		const html = renderPrivacyGuideHtml({ ...request, pane: "screenRecording", granted: { accessibility: true, screenRecording: false }, text: { ...request.text, hint: "<b>x</b>" } }, { kind: "image", dataUrl: "javascript:alert(1)" });
		expect(html).toContain("&#60;b&#62;x&#60;/b&#62;");
		expect(html).toContain('aria-label="Granted"');
		expect(html).not.toContain("<img");
		expect(html).toContain('class="placeholder"');
	});

	it("says CuaDriver is allowed and offers no drag on a pane it already holds", () => {
		const html = renderPrivacyGuideHtml({ ...request, granted: { accessibility: true, screenRecording: false } });
		expect(html).toContain("CuaDriver is allowed here");
		expect(html).toContain("Turn off its switch to revoke access.");
		expect(html).not.toContain("data-drag-app");
		expect(html).not.toContain("Drag CuaDriver into the list above");
	});

	it("paints in AO's scheme and colors, not the OS appearance", () => {
		const dark = renderPrivacyGuideHtml(request);
		expect(dark).toContain("color-scheme: dark; --bg: oklch(0.3 0.002 250); --fg: #fafafa; --muted: rgb(180 180 180)");
		expect(dark).not.toContain("prefers-color-scheme");
		const light = renderPrivacyGuideHtml({ ...request, appearance: { scheme: "light", colors: {} } });
		expect(light).toContain("color-scheme: light; --bg: oklch(1 0 0); --fg: oklch(0.18 0 0)");
	});

	it("shows a spinning gear while the icon loads and a still gear when there is none", () => {
		expect(renderPrivacyGuideHtml(request, { kind: "loading" })).toContain('class="placeholder loading"');
		const missing = renderPrivacyGuideHtml(request, { kind: "missing" });
		expect(missing).toContain('class="placeholder"');
		expect(missing).not.toContain("<img");
	});
});

describe("PrivacyGuide", () => {
	it("opens the chosen pane and docks under System Settings once it is seen in front", async () => {
		const { guide: subject, deps, window, emit } = guide();
		await subject.show(request);
		expect(deps.openExternal).toHaveBeenCalledWith(PRIVACY_PANE_URLS.accessibility);
		expect(deps.readIcon).toHaveBeenCalledWith("/Applications/CuaDriver.app");
		expect(window.showInactive).not.toHaveBeenCalled();
		await vi.waitFor(() => expect(window.html()).toContain('src="data:image/png;base64,AAAA"'));

		emit(front());
		expect(window.setBounds).toHaveBeenLastCalledWith({ x: 590, y: 846, width: 740, height: PRIVACY_GUIDE_HEIGHT });
		expect(window.showInactive).toHaveBeenCalledTimes(1);

		emit(front({ ...settingsWindow, x: 100 }));
		expect(window.setBounds).toHaveBeenLastCalledWith(expect.objectContaining({ x: 100 }));
	});

	it("hides while another app is in front and closes when System Settings quits", async () => {
		const { guide: subject, window, emit, stop } = guide();
		await subject.show(request);
		emit(front());
		emit({ ...front(), frontPid: 7 });
		expect(window.hide).toHaveBeenCalled();
		emit({ frontPid: 7, settingsPid: -1, bounds: null });
		expect(window.close).toHaveBeenCalled();
		expect(stop).toHaveBeenCalled();
	});

	it("parks the strip when System Settings cannot be followed", async () => {
		const { guide: subject, window, fail } = guide();
		await subject.show(request);
		fail();
		expect(window.setBounds).toHaveBeenLastCalledWith({ x: 660, y: 984, width: 600, height: PRIVACY_GUIDE_HEIGHT });
		expect(window.showInactive).toHaveBeenCalled();
	});

	it("switches pane when the user picks another one in the strip", async () => {
		const { guide: subject, deps, window } = guide();
		await subject.show(request);
		window.navigate("ao-privacy-guide://pane/screenRecording");
		await vi.waitFor(() => expect(deps.openExternal).toHaveBeenLastCalledWith(PRIVACY_PANE_URLS.screenRecording));
		expect(deps.createWindow).toHaveBeenCalledTimes(1);
		expect(deps.watchSettings).toHaveBeenCalledTimes(1);
	});

	it("moves to the next missing pane, then closes once everything is granted", async () => {
		const { guide: subject, deps, window, stop } = guide();
		await subject.show(request);
		await expect(subject.update({ accessibility: true, screenRecording: false })).resolves.toBe(true);
		expect(deps.openExternal).toHaveBeenLastCalledWith(PRIVACY_PANE_URLS.screenRecording);
		await expect(subject.update({ accessibility: true, screenRecording: true })).resolves.toBe(false);
		expect(window.close).toHaveBeenCalled();
		expect(stop).toHaveBeenCalled();
	});

	it("stays open when it was opened to review grants that were all on, and follows a revoke", async () => {
		const { guide: subject, deps, window } = guide();
		const allGranted = { accessibility: true, screenRecording: true };
		await subject.show({ ...request, granted: allGranted });
		expect(window.html()).toContain("CuaDriver is allowed here");
		await expect(subject.update(allGranted)).resolves.toBe(true);
		expect(window.close).not.toHaveBeenCalled();

		// Revoked on the shown pane: the strip turns back into a drop target, in place.
		await expect(subject.update({ accessibility: false, screenRecording: true })).resolves.toBe(true);
		expect(window.html()).toContain("data-drag-app");
		expect(deps.openExternal).toHaveBeenCalledTimes(1);
	});

	it("reports itself closed once its X or System Settings quitting took it down", async () => {
		const { guide: subject, window } = guide();
		await expect(subject.update({ accessibility: false, screenRecording: false })).resolves.toBe(false);
		await subject.show(request);
		window.navigate("ao-privacy-guide://close");
		await expect(subject.update({ accessibility: true, screenRecording: false })).resolves.toBe(false);

		const again = guide();
		await again.guide.show(request);
		again.emit(front());
		again.emit({ frontPid: 7, settingsPid: -1, bounds: null });
		await expect(again.guide.update({ accessibility: true, screenRecording: false })).resolves.toBe(false);
	});

	it("renders with a loading gear first, then swaps in the icon", async () => {
		const { guide: subject, deps, window } = guide();
		let resolveIcon: (value: string | undefined) => void = () => undefined;
		deps.readIcon.mockReturnValue(new Promise((resolve) => { resolveIcon = resolve; }));
		await subject.show(request);
		expect(window.html()).toContain('class="placeholder loading"');
		resolveIcon(undefined);
		await vi.waitFor(() => expect(window.html()).toContain('class="placeholder"'));
		expect(window.html()).not.toContain('class="placeholder loading"');
	});

	it("ignores a load that a newer render aborted", async () => {
		const { guide: subject, deps, window } = guide();
		let resolveIcon: (value: string | undefined) => void = () => undefined;
		deps.readIcon.mockReturnValue(new Promise((resolve) => { resolveIcon = resolve; }));
		let rejectFirst: (error: Error) => void = () => undefined;
		window.loadURL.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectFirst = reject; }));
		const shown = subject.show(request);
		await vi.waitFor(() => expect(window.loadURL).toHaveBeenCalledTimes(1));
		resolveIcon("data:image/png;base64,AAAA");
		await vi.waitFor(() => expect(window.loadURL).toHaveBeenCalledTimes(2));
		rejectFirst(new Error("ERR_ABORTED (-3)"));
		await expect(shown).resolves.toBeUndefined();
	});

	it("closes from the strip's close link", async () => {
		const { guide: subject, window } = guide();
		await subject.show(request);
		window.navigate("ao-privacy-guide://close");
		expect(window.close).toHaveBeenCalled();
	});
});
