import { describe, expect, it } from "vitest";
import type { DashboardSession } from "../api";
import {
	buildSidebarTree,
	capRows,
	floatAttention,
	projectActivity,
	projectLetter,
	projectTileColor,
	PROJECT_TILE_COLORS,
	sidebarIndicator,
	sidebarSessionKey,
	type SidebarProjectInput,
	type SidebarSessionInput,
	type SidebarSessionRow,
	type SidebarTreeInput,
} from "./sidebarModel";

const session = (over: Partial<SidebarSessionInput> = {}): SidebarSessionInput => ({
	id: "s1",
	projectId: "app",
	status: "working",
	mode: "chat",
	branch: null,
	issueId: null,
	issueTitle: null,
	userPrompt: null,
	displayName: "Session",
	summary: null,
	createdAt: "2026-11-21T10:00:00Z",
	lastActivityAt: "2026-11-21T10:00:00Z",
	hostId: "mac",
	hostName: "Mac",
	...over,
} as SidebarSessionInput);

const project = (id: string, over: Partial<SidebarProjectInput> = {}): SidebarProjectInput => ({ id, name: id, hostId: "mac", hostName: "Mac", ...over });

const input = (over: Partial<SidebarTreeInput> = {}): SidebarTreeInput => ({
	sessions: [],
	projects: [],
	showHosts: false,
	isUnread: () => false,
	isCloudHost: (hostId) => hostId === "orchestrator-cloud",
	...over,
});

describe("sidebarIndicator", () => {
	// The desktop's own rules (product-ui), so a row reads the same on both apps.
	it("spins while the agent works, ahead of any PR state", () => {
		expect(sidebarIndicator(session({ activity: "active", displayStatus: "Mergeable", kanbanColumn: "ready" }))).toBe("working");
		expect(sidebarIndicator(session({ provisionState: "provisioning", activity: null }))).toBe("working");
	});

	it("flags what needs the user, then the PR state", () => {
		expect(sidebarIndicator(session({ status: "needs_input", activity: "waiting_input" }))).toBe("attention");
		expect(sidebarIndicator(session({ activity: "idle", displayStatus: "CI failing", kanbanColumn: "needs_review" }))).toBe("attention");
		expect(sidebarIndicator(session({ activity: "idle", displayStatus: "Mergeable", kanbanColumn: "ready" }))).toBe("ready");
		expect(sidebarIndicator(session({ activity: "idle", displayStatus: "Merged", kanbanColumn: "ready" }))).toBe("merged");
		expect(sidebarIndicator(session({ activity: "idle", displayStatus: "Awaiting review", kanbanColumn: "needs_review" }))).toBe("review");
	});

	it("leaves an idle session to the quiet dot", () => {
		expect(sidebarIndicator(session({ status: "idle", activity: "idle" }))).toBeNull();
		// An activity this client does not know reads as no activity.
		expect(sidebarIndicator(session({ status: "idle", activity: "sleeping" }))).toBeNull();
	});
});

describe("buildSidebarTree", () => {
	it("groups sessions under their project, on their own machine", () => {
		const tree = buildSidebarTree(input({
			projects: [project("app"), project("app", { hostId: "pc", hostName: "PC" })],
			sessions: [session({ id: "a" }), session({ id: "b", hostId: "pc", hostName: "PC" })],
		}));
		expect(tree.projects.map((node) => [node.key, node.sessions.map((row) => row.session.id)])).toEqual([
			["mac:app", ["a"]],
			["pc:app", ["b"]],
		]);
	});

	it("lists agents with no project under Sessions, and pinned sessions under Pinned too", () => {
		const tree = buildSidebarTree(input({
			projects: [project("app")],
			sessions: [
				session({ id: "solo", projectId: "" }),
				session({ id: "late", isPinned: true, pinnedAt: "2026-11-21T12:00:00Z" }),
				session({ id: "early", isPinned: true, pinnedAt: "2026-11-21T11:00:00Z" }),
			],
		}));
		expect(tree.standalone.map((row) => row.session.id)).toEqual(["solo"]);
		expect(tree.pinned.map((row) => row.session.id)).toEqual(["early", "late"]);
		expect(tree.projects[0].sessions.map((row) => row.session.id)).toContain("late");
	});

	it("keeps orchestrators, archived sessions and scratch projects out of the lists", () => {
		const tree = buildSidebarTree(input({
			projects: [project("app"), project("scratch", { kind: "scratch" })],
			sessions: [
				session({ id: "orch", kind: "orchestrator" }),
				session({ id: "gone", isTerminated: true, status: "terminated" }),
				session({ id: "live" }),
			],
		}));
		expect(tree.projects.map((node) => node.key)).toEqual(["mac:app"]);
		expect(tree.projects[0].sessions.map((row) => row.session.id)).toEqual(["live"]);
	});

	it("lists newest first and floats the sessions that need you", () => {
		const tree = buildSidebarTree(input({
			projects: [project("app")],
			sessions: [
				session({ id: "old", createdAt: "2026-11-20T10:00:00Z" }),
				session({ id: "new", createdAt: "2026-11-21T10:00:00Z" }),
				session({ id: "stuck", createdAt: "2026-11-19T10:00:00Z", status: "needs_input", activity: "waiting_input" }),
			],
		}));
		expect(tree.projects[0].sessions.map((row) => row.session.id)).toEqual(["stuck", "new", "old"]);
	});

	it("marks changed sessions unread, except the one on screen and any with an indicator", () => {
		const tree = buildSidebarTree(input({
			projects: [project("app")],
			sessions: [session({ id: "seen", activity: "idle", status: "idle" }), session({ id: "open", activity: "idle", status: "idle" }), session({ id: "busy", activity: "active" })],
			activeKey: sidebarSessionKey({ id: "open", hostId: "mac" }),
			isUnread: () => true,
		}));
		const unread = Object.fromEntries(tree.projects[0].sessions.map((row) => [row.session.id, row.unread]));
		expect(unread).toEqual({ seen: true, open: false, busy: false });
	});

	it("names the machine only when several environments are listed, never for Cloud", () => {
		const sessions = [session({ id: "a" }), session({ id: "c", hostId: "orchestrator-cloud", hostName: "Cloud" })];
		const projects = [project("app"), project("app", { hostId: "orchestrator-cloud", hostName: "Cloud" })];
		const one = buildSidebarTree(input({ sessions, projects }));
		expect(one.projects[0].hostLabel).toBeUndefined();
		const many = buildSidebarTree(input({ sessions, projects, showHosts: true }));
		expect(many.projects.map((node) => [node.hostLabel, node.cloud])).toEqual([["Mac", false], [undefined, true]]);
		expect(many.projects[1].sessions[0].cloud).toBe(true);
	});

	it("searches session titles and project names", () => {
		const projects = [project("billing"), project("web")];
		const sessions = [
			session({ id: "b1", projectId: "billing", displayName: "Retry webhooks" }),
			session({ id: "w1", projectId: "web", displayName: "Fix login" }),
			session({ id: "w2", projectId: "web", displayName: "Dark mode" }),
		];
		// A project that matches by name keeps every session.
		const byProject = buildSidebarTree(input({ projects, sessions, query: "bill" }));
		expect(byProject.projects.map((node) => [node.project.id, node.sessions.length])).toEqual([["billing", 1]]);
		// Otherwise only the matching sessions, and projects without one drop out.
		const bySession = buildSidebarTree(input({ projects, sessions, query: "LOGIN" }));
		expect(bySession.projects.map((node) => [node.project.id, node.sessions.map((row) => row.session.id)])).toEqual([["web", ["w1"]]]);
	});
});

describe("projectActivity", () => {
	const row = (over: Partial<SidebarSessionRow>): SidebarSessionRow => ({ key: "k", session: session(), title: "t", indicator: null, unread: false, at: undefined, cloud: false, ...over });

	it("flags attention first, then work in progress, then an unopened change", () => {
		expect(projectActivity([row({ indicator: "working" }), row({ indicator: "attention" })])).toBe("attention");
		expect(projectActivity([row({ unread: true }), row({ indicator: "working" })])).toBe("working");
		expect(projectActivity([row({ unread: true })])).toBe("unread");
		expect(projectActivity([row({ indicator: "ready" })])).toBeNull();
	});
});

describe("floatAttention", () => {
	const row = (key: string, indicator: SidebarSessionRow["indicator"] = null) => ({ key, indicator } as SidebarSessionRow);
	it("keeps the order when nothing, or everything, needs you", () => {
		expect(floatAttention([row("a"), row("b")]).map((item) => item.key)).toEqual(["a", "b"]);
		expect(floatAttention([row("a", "attention"), row("b", "attention")]).map((item) => item.key)).toEqual(["a", "b"]);
	});
});

describe("capRows", () => {
	const items = Array.from({ length: 8 }, (_, index) => ({ key: String(index) }));
	it("caps a list behind Show more and lifts the cap on request", () => {
		expect(capRows(items, 6, false)).toEqual({ listed: items.slice(0, 6), hiddenCount: 2 });
		expect(capRows(items, 6, true).listed).toHaveLength(8);
		expect(capRows(items.slice(0, 3), 6, false)).toEqual({ listed: items.slice(0, 3), hiddenCount: 0 });
	});

	// Desktop's useShowMoreCap: the open row is never hidden behind Show more.
	it("lifts the cap when the active row sits past it", () => {
		expect(capRows(items, 6, false, "7").listed).toHaveLength(8);
		expect(capRows(items, 6, false, "2").listed).toHaveLength(6);
	});
});

describe("project tiles", () => {
	it("draws the desktop's first-letter tile in a stable colour", () => {
		expect(projectLetter("web-dashboard")).toBe("W");
		expect(projectLetter("_api")).toBe("A");
		expect(projectLetter("—")).toBe("?");
		expect(projectTileColor("web")).toBe(projectTileColor("web"));
		expect(PROJECT_TILE_COLORS).toContain(projectTileColor("anything"));
	});
});

// Keep the fixture honest: the builder's input is the store's hosted row.
const _typecheck: DashboardSession = session();
void _typecheck;
