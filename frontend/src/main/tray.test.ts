import { afterEach, describe, expect, it, vi } from "vitest";
import type { TraySessionEntry } from "../shared/tray";

type MenuItem = {
	label?: string;
	sublabel?: string;
	enabled?: boolean;
	type?: string;
	role?: string;
	click?: () => void;
	submenu?: MenuItem[];
};

const { FakeTray, trayInstances } = vi.hoisted(() => {
	const trayInstances: FakeTray[] = [];
	class FakeTray {
		title = "";
		tooltip = "";
		template: MenuItem[] = [];
		destroyed = false;
		constructor(public icon: unknown) {
			trayInstances.push(this);
		}
		setTitle(title: string) {
			this.title = title;
		}
		setToolTip(tooltip: string) {
			this.tooltip = tooltip;
		}
		setContextMenu(menu: { template: MenuItem[] }) {
			this.template = menu.template;
		}
		destroy() {
			this.destroyed = true;
		}
	}
	return { FakeTray, trayInstances };
});

const setTemplateImage = vi.hoisted(() => vi.fn());

vi.mock("electron", () => ({
	app: { isPackaged: false, getVersion: () => "0.0.0-test" },
	nativeImage: { createFromPath: () => ({ isEmpty: () => false, setTemplateImage }) },
	Tray: FakeTray,
	Menu: { buildFromTemplate: (template: MenuItem[]) => ({ template }) },
}));

vi.mock("node:fs", async (importOriginal) => {
	const actual = await importOriginal<typeof import("node:fs")>();
	return { ...actual, existsSync: () => true };
});

import { createTrayController } from "./tray";

function entry(overrides: Partial<TraySessionEntry> & { sessionId: string }): TraySessionEntry {
	return { projectId: "proj-1", projectName: "note-tauri", title: overrides.sessionId, zone: "action", ...overrides };
}

function setup() {
	const openSession = vi.fn();
	const focusWindow = vi.fn();
	const quitCompletely = vi.fn();
	const controller = createTrayController({ focusWindow, openSession, quitCompletely, locale: "en" });
	if (!controller) throw new Error("expected a tray controller");
	const tray = trayInstances[trayInstances.length - 1];
	return { controller, tray, openSession, focusWindow, quitCompletely };
}

const sessionItems = (tray: { template: MenuItem[] }) =>
	tray.template.filter(
		(item) =>
			typeof item.click === "function" &&
			item.label !== "Show Agent Orchestrator" &&
			item.label !== "Quit AO Completely",
	);

afterEach(() => {
	trayInstances.length = 0;
	vi.clearAllMocks();
});

describe("createTrayController", () => {
	it("renders an empty state with no title before any session needs attention", () => {
		const { tray } = setup();
		expect(tray.title).toBe("");
		expect(tray.template.some((i) => i.label === "No sessions need attention" && i.enabled === false)).toBe(true);
		expect(tray.template.some((i) => i.label === "Quit AO Completely")).toBe(true);
	});

	it("hands the explicit quit action to the quitCompletely delegate", () => {
		const { tray, quitCompletely } = setup();
		const quit = tray.template.find((item) => item.label === "Quit AO Completely");
		expect(quit?.role).toBeUndefined();
		quit?.click?.();
		expect(quitCompletely).toHaveBeenCalledTimes(1);
	});

	it("hands the show action to the focusWindow delegate", () => {
		const { tray, focusWindow } = setup();
		tray.template.find((item) => item.label === "Show Agent Orchestrator")?.click?.();
		expect(focusWindow).toHaveBeenCalledTimes(1);
	});

	it("uses grammatically correct singular for exactly one attention session", () => {
		const { controller, tray } = setup();
		controller.setState({ sessions: [entry({ sessionId: "s1", zone: "action" })] });
		expect(tray.tooltip).toBe("1 session needs attention");
	});

	it("shows the count and orders merge-ready sessions above needs-you", () => {
		const { controller, tray } = setup();
		controller.setState({
			sessions: [
				entry({ sessionId: "needs", title: "needs you", zone: "action" }),
				entry({ sessionId: "ready", title: "ready", zone: "merge" }),
			],
		});
		expect(tray.title).toBe("2");
		expect(tray.tooltip).toBe("2 sessions need attention");
		const labels = tray.template.map((i) => i.label);
		expect(labels).toContain("Ready to merge");
		expect(labels).toContain("Needs you");
		expect(labels.indexOf("Ready to merge")).toBeLessThan(labels.indexOf("Needs you"));
		expect(sessionItems(tray).map((i) => i.label)).toEqual(["ready  ·  note-tauri", "needs you  ·  note-tauri"]);
	});

	it("hands a session click to the openSession delegate", () => {
		const { controller, tray, openSession } = setup();
		controller.setState({ sessions: [entry({ sessionId: "s1", title: "one" })] });
		sessionItems(tray)[0].click?.();
		expect(openSession).toHaveBeenCalledWith({ projectId: "proj-1", sessionId: "s1" });
	});

	it("marks the icon as a macOS template so the menu bar can tint it", () => {
		setup();
		expect(setTemplateImage).toHaveBeenCalledWith(true);
	});

	it("caps the menu and notes the overflow", () => {
		const { controller, tray } = setup();
		const many = Array.from({ length: 11 }, (_, i) => entry({ sessionId: `s${i}`, title: `s${i}` }));
		controller.setState({ sessions: many });
		expect(sessionItems(tray)).toHaveLength(8);
		const more = tray.template.find((i) => i.label?.startsWith("More"));
		expect(more?.submenu).toHaveLength(3);
	});

	it("clears back to the empty state", () => {
		const { controller, tray } = setup();
		controller.setState({ sessions: [entry({ sessionId: "s1" })] });
		expect(tray.title).toBe("1");
		controller.clear();
		expect(tray.title).toBe("");
		expect(tray.template.some((i) => i.label === "No sessions need attention")).toBe(true);
	});

	it("destroys the tray on dispose", () => {
		const { controller, tray } = setup();
		controller.dispose();
		expect(tray.destroyed).toBe(true);
	});

	it("relocalizes menu labels when setLocale is called", () => {
		const { controller, tray } = setup();
		controller.setState({ sessions: [entry({ sessionId: "s1", zone: "action" })] });
		expect(tray.template.some((i) => i.label === "Needs you")).toBe(true);

		controller.setLocale("zh-CN");
		expect(tray.tooltip).toBe("1 个会话需要关注");
		expect(tray.template.some((i) => i.label === "需要你处理")).toBe(true);
		expect(tray.template.some((i) => i.label === "显示 Agent Orchestrator")).toBe(true);
		expect(tray.template.some((i) => i.label === "完全退出 AO")).toBe(true);
	});
});
