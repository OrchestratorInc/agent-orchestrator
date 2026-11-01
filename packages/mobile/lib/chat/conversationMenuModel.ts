import type { IOSSymbol } from "../symbols";
import type { MaterialDrawableName } from "../toolbarIcon.types";
import { ARCHIVE_SESSION_HINT } from "../worker-action-model";

/**
 * What the conversation's actions page offers. The switch to Terminal UI, the
 * preview and the session's files are not here: they are the header's own
 * buttons, as on the terminal (see `sessionHeaderButtons`).
 */
export type ConversationMenuAction =
	| "shell"
	| "pull_requests"
	| "map"
	| "refresh"
	| "usage"
	| "rename"
	| "pin"
	| "compact"
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
		{ title: "Workspace", actions: ["shell", "pull_requests"] },
		{
			title: "Conversation",
			actions: [
				"map",
				"refresh",
				...(hasUsage ? ["usage" as const] : []),
				...(canRename ? ["rename" as const] : []),
				...(canPin ? ["pin" as const] : []),
				...(canCompact ? ["compact" as const] : []),
			],
		},
		// A section only when it has something in it: no page shows an empty group.
		...(canReloadMcp ? [{ title: "Agent" as const, actions: ["reload_mcp" as const] }] : []),
		...(canDelete ? [{ title: "Session" as const, actions: ["delete" as const] }] : []),
	];
}

/** Everything the actions page shows, as plain values, so it can be compared and kept still. */
export type ConversationMenuState = {
	canRename: boolean;
	canPin: boolean;
	canCompact: boolean;
	canReloadMcp: boolean;
	canDelete: boolean;
	hasUsage: boolean;
	pinned: boolean;
	openingShell: boolean;
	refreshing: boolean;
	compacting: boolean;
	mcpReloading: boolean;
	/** A turn is running or queued, which compaction and an MCP reload both wait for. */
	turnInFlight: boolean;
};

export type ConversationMenuItem = {
	action: ConversationMenuAction;
	label: string;
	/** A second line under the label. */
	subtitle?: string;
	disabled?: boolean;
	destructive?: boolean;
	/** The row's glyph: an SF Symbol on iOS, a bundled Material Symbol on Android. */
	icon: { ios: IOSSymbol; android: MaterialDrawableName };
};

export type ConversationMenuGroup = { title: ConversationMenuSection["title"]; items: ConversationMenuItem[] };

/**
 * The conversation's actions page: every action, grouped as the sections
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
		case "shell": return { action, label: state.openingShell ? "Opening shell…" : "Open worktree shell", disabled: state.openingShell, icon: { ios: "terminal", android: "terminal" } };
		case "pull_requests": return { action, label: "PR Review", icon: { ios: "arrow.triangle.pull", android: "rate_review" } };
		case "map": return { action, label: "Conversation history", icon: { ios: "list.bullet", android: "history" } };
		case "refresh": return { action, label: state.refreshing ? "Refreshing conversation…" : "Refresh conversation", disabled: state.refreshing, icon: { ios: "arrow.clockwise", android: "refresh" } };
		case "usage": return { action, label: "Context & usage", icon: { ios: "chart.bar", android: "data_usage" } };
		case "rename": return { action, label: "Rename conversation", icon: { ios: "pencil", android: "edit" } };
		case "pin": return { action, label: state.pinned ? "Unpin worker" : "Pin worker", icon: state.pinned ? { ios: "pin.slash", android: "keep_off" } : { ios: "pin", android: "keep" } };
		case "compact": return { action, label: state.compacting ? "Compacting history…" : "Compact history", disabled: state.turnInFlight || state.compacting, icon: { ios: "arrow.down.right.and.arrow.up.left", android: "compress" } };
		case "reload_mcp": return { action, label: state.mcpReloading ? "Reloading MCP servers…" : "Reload MCP servers", disabled: state.turnInFlight || state.mcpReloading, icon: { ios: "wrench.and.screwdriver", android: "build" } };
		case "delete": return { action, label: "Archive session", subtitle: ARCHIVE_SESSION_HINT, destructive: true, icon: { ios: "archivebox", android: "archive" } };
	}
}

export function normalizeConversationTitle(value: string): string | undefined {
	const title = value.trim();
	return title || undefined;
}
