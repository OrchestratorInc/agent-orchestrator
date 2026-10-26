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
	modelLabel: "",
	openingShell: false,
	refreshing: false,
	compacting: false,
	mcpReloading: false,
	turnInFlight: false,
	interfaceSupported: true,
	interfaceSwitching: false,
};

const item = (state: ConversationMenuState, action: string) =>
	conversationMenuGroups(state).flatMap((group) => group.items).find((entry) => entry.action === action);

describe("conversation overflow menu", () => {
	it("groups every supported session action by user intent", () => {
		const sections = conversationMenuSections({
			canRename: true,
			canPin: true,
			canCompact: true,
			canReloadMcp: true,
			canDelete: true,
			hasUsage: true,
		});

		expect(sections).toEqual([
			{ title: "Workspace", actions: ["files", "shell", "preview", "pull_requests"] },
			{ title: "Conversation", actions: ["map", "refresh", "settings", "usage", "rename", "pin", "compact"] },
			{ title: "Agent", actions: ["terminal_ui", "reload_mcp"] },
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

		expect(sections[1]?.actions).toEqual(["map", "refresh", "settings"]);
		expect(sections[2]?.actions).toEqual(["terminal_ui"]);
		// An orchestrator has no delete here, so the section goes rather than
		// rendering an empty group.
		expect(sections.some((section) => section.title === "Session")).toBe(false);
	});

	it("labels each action and gives it an SF Symbol", () => {
		const groups = conversationMenuGroups(idle);
		expect(groups.map((group) => group.title)).toEqual(["Workspace", "Conversation", "Agent", "Session"]);
		expect(groups.flatMap((group) => group.items.map((entry) => entry.label))).toEqual([
			"Review changes",
			"Open worktree shell",
			"Open preview",
			"PR Review",
			"Conversation history",
			"Refresh conversation",
			"Turn settings",
			"Context & usage",
			"Rename conversation",
			"Pin worker",
			"Compact history",
			"Open Terminal UI",
			"Reload MCP servers",
			"Archive session",
		]);
		for (const group of groups) for (const entry of group.items) expect(entry.icon).toBeTruthy();
	});

	it("marks Archive destructive and explains it", () => {
		expect(item(idle, "delete")).toMatchObject({ destructive: true, subtitle: ARCHIVE_SESSION_HINT });
		expect(conversationMenuGroups(idle).flatMap((group) => group.items).filter((entry) => entry.destructive)).toHaveLength(1);
	});

	it("names the turn settings' model under the item", () => {
		expect(item({ ...idle, modelLabel: "Opus · high" }, "settings")?.subtitle).toBe("Opus · high");
		expect(item(idle, "settings")?.subtitle).toBeUndefined();
	});

	it("says an action is running and holds it until it finishes", () => {
		const busy = { ...idle, openingShell: true, refreshing: true, compacting: true, mcpReloading: true, interfaceSwitching: true };
		expect(item(busy, "shell")).toMatchObject({ label: "Opening shell…", disabled: true });
		expect(item(busy, "refresh")).toMatchObject({ label: "Refreshing conversation…", disabled: true });
		expect(item(busy, "compact")).toMatchObject({ label: "Compacting history…", disabled: true });
		expect(item(busy, "reload_mcp")).toMatchObject({ label: "Reloading MCP servers…", disabled: true });
		expect(item(busy, "terminal_ui")).toMatchObject({ label: "Switching interface…", disabled: true });
	});

	it("holds compaction and MCP reloads while a turn runs", () => {
		const running = { ...idle, turnInFlight: true };
		expect(item(running, "compact")?.disabled).toBe(true);
		expect(item(running, "reload_mcp")?.disabled).toBe(true);
		expect(item(running, "refresh")?.disabled).toBe(false);
	});

	// Android menus have no subtitle, so the item stays choosable and the screen
	// explains on tap.
	it("keeps Terminal UI choosable when the agent cannot hand off, with the reason", () => {
		const unsupported = item({ ...idle, interfaceSupported: false, interfaceReason: "Codex cannot resume here" }, "terminal_ui");
		expect(unsupported).toMatchObject({ disabled: false, subtitle: "Codex cannot resume here" });
		expect(item({ ...idle, interfaceSupported: false }, "terminal_ui")?.subtitle).toBe("This agent does not support a compatible handoff");
	});

	it("offers Unpin for a pinned worker", () => {
		expect(item({ ...idle, pinned: true }, "pin")).toMatchObject({ label: "Unpin worker", icon: "pin.slash" });
	});
});

describe("conversation rename", () => {
	it("trims a useful title and rejects an empty one", () => {
		expect(normalizeConversationTitle("  Release follow-up  ")).toBe("Release follow-up");
		expect(normalizeConversationTitle("   ")).toBeUndefined();
	});
});
