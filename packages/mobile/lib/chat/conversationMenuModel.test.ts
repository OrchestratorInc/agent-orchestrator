import { describe, expect, it } from "vitest";
import { ARCHIVE_SESSION_HINT } from "../worker-action-model";
import { conversationMenuGroups, conversationMenuSections, normalizeConversationTitle, type ConversationMenuState } from "./conversationMenuModel";

const idle: ConversationMenuState = {
	canRename: true,
	canPin: true,
	canCompact: true,
	canReloadMcp: true,
	canDelete: true,
	hasUsage: true,
	pinned: false,
	openingShell: false,
	refreshing: false,
	compacting: false,
	mcpReloading: false,
	turnInFlight: false,
};

const item = (state: ConversationMenuState, action: string) =>
	conversationMenuGroups(state).flatMap((group) => group.items).find((entry) => entry.action === action);

describe("conversation actions page", () => {
	it("groups every supported session action by user intent", () => {
		const sections = conversationMenuSections({
			canRename: true,
			canPin: true,
			canCompact: true,
			canReloadMcp: true,
			canDelete: true,
			hasUsage: true,
		});

		// Terminal UI, the preview and the files are the header's buttons, not rows here.
		expect(sections).toEqual([
			{ title: "Workspace", actions: ["shell", "pull_requests"] },
			{ title: "Conversation", actions: ["map", "refresh", "usage", "rename", "pin", "compact"] },
			{ title: "Agent", actions: ["reload_mcp"] },
			// Last, and alone: the only entry here that destroys something.
			{ title: "Session", actions: ["delete"] },
		]);
	});

	it("omits capability-gated maintenance actions without disturbing the core menu", () => {
		const sections = conversationMenuSections({
			canRename: false,
			canPin: false,
			canCompact: false,
			canReloadMcp: false,
			canDelete: false,
			hasUsage: false,
		});

		expect(sections[1]?.actions).toEqual(["map", "refresh"]);
		// A section with nothing left in it goes rather than rendering an empty
		// group: no MCP reload, and an orchestrator has no archive here.
		expect(sections.map((section) => section.title)).toEqual(["Workspace", "Conversation"]);
	});

	it("labels each action and gives it a glyph on each platform", () => {
		const groups = conversationMenuGroups(idle);
		expect(groups.map((group) => group.title)).toEqual(["Workspace", "Conversation", "Agent", "Session"]);
		expect(groups.flatMap((group) => group.items.map((entry) => entry.label))).toEqual([
			"Open worktree shell",
			"PR Review",
			"Conversation history",
			"Refresh conversation",
			"Context & usage",
			"Rename conversation",
			"Pin worker",
			"Compact history",
			"Reload MCP servers",
			"Archive session",
		]);
		for (const group of groups) for (const entry of group.items) expect(entry.icon).toEqual({ ios: expect.any(String), android: expect.any(String) });
	});

	it("marks Archive destructive and explains it", () => {
		expect(item(idle, "delete")).toMatchObject({ destructive: true, subtitle: ARCHIVE_SESSION_HINT });
		expect(conversationMenuGroups(idle).flatMap((group) => group.items).filter((entry) => entry.destructive)).toHaveLength(1);
	});

	it("says an action is running and holds it until it finishes", () => {
		const busy = { ...idle, openingShell: true, refreshing: true, compacting: true, mcpReloading: true };
		expect(item(busy, "shell")).toMatchObject({ label: "Opening shell…", disabled: true });
		expect(item(busy, "refresh")).toMatchObject({ label: "Refreshing conversation…", disabled: true });
		expect(item(busy, "compact")).toMatchObject({ label: "Compacting history…", disabled: true });
		expect(item(busy, "reload_mcp")).toMatchObject({ label: "Reloading MCP servers…", disabled: true });
	});

	it("holds compaction and MCP reloads while a turn runs", () => {
		const running = { ...idle, turnInFlight: true };
		expect(item(running, "compact")?.disabled).toBe(true);
		expect(item(running, "reload_mcp")?.disabled).toBe(true);
		expect(item(running, "refresh")?.disabled).toBe(false);
	});

	it("offers Unpin for a pinned worker", () => {
		expect(item({ ...idle, pinned: true }, "pin")).toMatchObject({ label: "Unpin worker", icon: { ios: "pin.slash", android: "keep_off" } });
	});
});

describe("conversation rename", () => {
	it("trims a useful title and rejects an empty one", () => {
		expect(normalizeConversationTitle("  Release follow-up  ")).toBe("Release follow-up");
		expect(normalizeConversationTitle("   ")).toBeUndefined();
	});
});
