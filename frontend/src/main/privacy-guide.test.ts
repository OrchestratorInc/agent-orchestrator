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
	text: {
		drag: "Drag CuaDriver into the list above",
		hint: "Already in the list? Turn on its switch.",
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
		expect(coercePrivacyGuideRequest(null)).toBeUndefined();
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
		expect(html).toContain('class="app" draggable="true" data-drag-app');
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
		await subject.update({ accessibility: true, screenRecording: false });
		expect(deps.openExternal).toHaveBeenLastCalledWith(PRIVACY_PANE_URLS.screenRecording);
		await subject.update({ accessibility: true, screenRecording: true });
		expect(window.close).toHaveBeenCalled();
		expect(stop).toHaveBeenCalled();
	});

	it("stays open when it was opened to review grants that were all on", async () => {
		const { guide: subject, window } = guide();
		const allGranted = { accessibility: true, screenRecording: true };
		await subject.show({ ...request, granted: allGranted });
		await subject.update(allGranted);
		expect(window.close).not.toHaveBeenCalled();
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
