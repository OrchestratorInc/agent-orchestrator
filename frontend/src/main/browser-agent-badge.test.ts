import { afterEach, describe, expect, it, vi } from "vitest";
import {
	AGENT_BADGE_SHOW_MS,
	AGENT_BADGE_VIEW_PX,
	agentBadgeBounds,
	agentBadgeDocument,
	createAgentBadge,
	type AgentBadgeView,
} from "./browser-agent-badge";

function fakeView() {
	const listeners = new Map<string, (...args: never[]) => void>();
	const view = {
		setBounds: vi.fn(),
		setVisible: vi.fn(),
		setBackgroundColor: vi.fn(),
		webContents: {
			loadURL: vi.fn(async () => undefined),
			on: vi.fn((event: string, listener: (...args: never[]) => void) => listeners.set(event, listener)),
			close: vi.fn(),
		},
	};
	return { view, listeners };
}

function setup() {
	const { view, listeners } = fakeView();
	const attach = vi.fn();
	const detach = vi.fn();
	const badge = createAgentBadge({ create: () => view as AgentBadgeView, attach, detach });
	return { badge, view, listeners, attach, detach };
}

const page = { x: 10, y: 20, width: 800, height: 600 };

afterEach(() => {
	vi.useRealTimers();
});

describe("agent badge", () => {
	it("is the mascot with CSS animation only: no script, motion reduced on request", () => {
		const html = agentBadgeDocument();
		expect(html).toContain("<svg");
		expect(html).not.toMatch(/<script/i);
		expect(html).toContain("prefers-reduced-motion");
	});

	it("sits at the page's bottom-left corner, and nowhere on a page too small to spare it", () => {
		expect(agentBadgeBounds(page)).toEqual({
			x: 10,
			y: 20 + 600 - AGENT_BADGE_VIEW_PX,
			width: AGENT_BADGE_VIEW_PX,
			height: AGENT_BADGE_VIEW_PX,
		});
		expect(agentBadgeBounds({ x: 0, y: 0, width: 150, height: 600 })).toBeUndefined();
	});

	it("pops up over the page, stays a few seconds, then leaves", () => {
		vi.useFakeTimers();
		const { badge, view, attach } = setup();
		const owner = {};
		badge.show(owner, page);
		expect(view.setBackgroundColor).toHaveBeenCalledWith("#00000000");
		expect(view.webContents.loadURL).toHaveBeenCalledWith(expect.stringMatching(/^data:text\/html/));
		expect(view.setBounds).toHaveBeenLastCalledWith(agentBadgeBounds(page));
		expect(view.setVisible).toHaveBeenLastCalledWith(true);
		expect(attach).toHaveBeenCalledWith(view);
		expect(badge.showing(owner)).toBe(true);

		vi.advanceTimersByTime(AGENT_BADGE_SHOW_MS + 200);
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
		expect(badge.showing(owner)).toBe(false);
	});

	it("leaves the moment the person's pointer reaches it", () => {
		const { badge, view, listeners } = setup();
		badge.show("s", page);
		listeners.get("before-mouse-event")!();
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
		expect(badge.showing("s")).toBe(false);
	});

	it("follows its own page, and leaves when the page gets too small", () => {
		const { badge, view } = setup();
		badge.show("s", page);
		badge.follow("other", { x: 0, y: 0, width: 900, height: 900 });
		expect(view.setBounds).toHaveBeenLastCalledWith(agentBadgeBounds(page));
		const moved = { x: 40, y: 50, width: 700, height: 500 };
		badge.follow("s", moved);
		expect(view.setBounds).toHaveBeenLastCalledWith(agentBadgeBounds(moved));
		badge.follow("s", { x: 0, y: 0, width: 100, height: 100 });
		expect(view.setVisible).toHaveBeenLastCalledWith(false);
	});

	it("reuses one view and closes it on dispose", () => {
		const { view } = fakeView();
		const create = vi.fn(() => view as AgentBadgeView);
		const detach = vi.fn();
		const badge = createAgentBadge({ create, attach: vi.fn(), detach });
		badge.show("a", page);
		badge.show("b", page);
		expect(create).toHaveBeenCalledTimes(1);
		badge.dispose();
		expect(detach).toHaveBeenCalledWith(view);
		expect(view.webContents.close).toHaveBeenCalled();
	});
});
