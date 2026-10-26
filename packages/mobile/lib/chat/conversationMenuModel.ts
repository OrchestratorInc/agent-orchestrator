import type { IOSSymbol } from "../symbols";
import { ARCHIVE_SESSION_HINT } from "../worker-action-model";

export type ConversationMenuAction =
	| "files"
	| "shell"
	| "preview"
	| "pull_requests"
	| "map"
	| "refresh"
	| "settings"
	| "usage"
	| "rename"
	| "pin"
	| "compact"
	| "terminal_ui"
	| "reload_mcp"
	| "delete";

export type ConversationMenuSection = {
	// Archive acts on the session, not the conversation, so it sits in a section
	// of its own. The internal action id remains `delete` for compatibility with
	// the worker row's actions, but the operation is restorable.
	title: "Workspace" | "Conversation" | "Agent" | "Session";
	actions: ConversationMenuAction[];
};

export function conversationMenuSections({
	canRename,
	canPin,
	canCompact,
	canReloadMcp,
	canDelete,
	hasUsage,
}: {
	canRename: boolean;
	canPin: boolean;
	canCompact: boolean;
	canReloadMcp: boolean;
	canDelete: boolean;
	/** The agent reported token usage or account limits, so there are details to show. */
	hasUsage: boolean;
}): ConversationMenuSection[] {
	return [
		{ title: "Workspace", actions: ["files", "shell", "preview", "pull_requests"] },
		{
			title: "Conversation",
			actions: [
				"map",
				"refresh",
				"settings",
				...(hasUsage ? ["usage" as const] : []),
				...(canRename ? ["rename" as const] : []),
				...(canPin ? ["pin" as const] : []),
				...(canCompact ? ["compact" as const] : []),
			],
		},
		{
			title: "Agent",
			actions: [
				"terminal_ui",
				...(canReloadMcp ? ["reload_mcp" as const] : []),
			],
		},
		...(canDelete ? [{ title: "Session" as const, actions: ["delete" as const] }] : []),
	];
}

/** Everything the conversation menu shows, as plain values, so it can be compared and kept still. */
export type ConversationMenuState = {
	canRename: boolean;
	canPin: boolean;
	canCompact: boolean;
	canReloadMcp: boolean;
	canDelete: boolean;
	hasUsage: boolean;
	pinned: boolean;
	/** What the Turn settings item names; empty when nothing is known. */
	modelLabel: string;
	openingShell: boolean;
	refreshing: boolean;
	compacting: boolean;
	mcpReloading: boolean;
	/** A turn is running or queued, which compaction and an MCP reload both wait for. */
	turnInFlight: boolean;
	interfaceSupported: boolean;
	interfaceReason?: string;
	interfaceSwitching: boolean;
};

export type ConversationMenuItem = {
	action: ConversationMenuAction;
	label: string;
	/** A second line under the label. iOS menus draw it; Android's have no place for it. */
	subtitle?: string;
	disabled?: boolean;
	destructive?: boolean;
	/** The SF Symbol iOS draws beside the label. Android's overflow menu is text only. */
	icon: IOSSymbol;
};

export type ConversationMenuGroup = { title: ConversationMenuSection["title"]; items: ConversationMenuItem[] };

/**
 * The conversation's overflow menu: every action, grouped as the sections
 * above, with the label, state and glyph it shows right now. An action that is
 * running says so in its label and cannot be chosen again until it finishes.
 */
export function conversationMenuGroups(state: ConversationMenuState): ConversationMenuGroup[] {
	return conversationMenuSections(state).map((section) => ({
		title: section.title,
		items: section.actions.map((action) => conversationMenuItem(action, state)),
	}));
}

function conversationMenuItem(action: ConversationMenuAction, state: ConversationMenuState): ConversationMenuItem {
	switch (action) {
		case "files": return { action, label: "Review changes", icon: "plus.forwardslash.minus" };
		case "shell": return { action, label: state.openingShell ? "Opening shell…" : "Open worktree shell", disabled: state.openingShell, icon: "terminal" };
		case "preview": return { action, label: "Open preview", icon: "globe" };
		case "pull_requests": return { action, label: "PR Review", icon: "arrow.triangle.pull" };
		case "map": return { action, label: "Conversation history", icon: "list.bullet" };
		case "refresh": return { action, label: state.refreshing ? "Refreshing conversation…" : "Refresh conversation", disabled: state.refreshing, icon: "arrow.clockwise" };
		case "settings": return { action, label: "Turn settings", subtitle: state.modelLabel || undefined, icon: "slider.horizontal.3" };
		case "usage": return { action, label: "Context & usage", icon: "chart.bar" };
		case "rename": return { action, label: "Rename conversation", icon: "pencil" };
		case "pin": return { action, label: state.pinned ? "Unpin worker" : "Pin worker", icon: state.pinned ? "pin.slash" : "pin" };
		case "compact": return { action, label: state.compacting ? "Compacting history…" : "Compact history", disabled: state.turnInFlight || state.compacting, icon: "arrow.down.right.and.arrow.up.left" };
		// Left enabled when the agent cannot hand off, so choosing it explains why;
		// Android's menu has no subtitle to carry the reason.
		case "terminal_ui": return {
			action,
			label: state.interfaceSwitching ? "Switching interface…" : "Open Terminal UI",
			subtitle: state.interfaceSupported ? undefined : state.interfaceReason || "This agent does not support a compatible handoff",
			disabled: state.interfaceSwitching,
			icon: "rectangle.2.swap",
		};
		case "reload_mcp": return { action, label: state.mcpReloading ? "Reloading MCP servers…" : "Reload MCP servers", disabled: state.turnInFlight || state.mcpReloading, icon: "wrench.and.screwdriver" };
		case "delete": return { action, label: "Archive session", subtitle: ARCHIVE_SESSION_HINT, destructive: true, icon: "archivebox" };
	}
}

export function normalizeConversationTitle(value: string): string | undefined {
	const title = value.trim();
	return title || undefined;
}
