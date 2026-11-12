import type { IOSSymbol } from "../symbols";
import type { MaterialDrawableName } from "../toolbarIcon.types";
import { ARCHIVE_SESSION_HINT } from "../worker-action-model";

/**
 * What the conversation's actions sheet offers, and nothing more: the
 * worktree's terminal, the preview and the artifacts, pinning the worker and reviewing its
 * PR, compacting the history (its context use drawn as a ring), switching the
 * session to Terminal UI, and archiving it. Artifacts and PR Review show only once there is something to
 * open: on an empty session they led to an empty page.
 */
export type ConversationMenuAction = "shell" | "preview" | "artifacts" | "pin" | "pull_requests" | "compact" | "switch_tui" | "delete" | CloudConversationMenuAction;

/**
 * What only an Orchestrator Cloud session's sheet offers: its agent's own
 * terminal (a Terminal UI session's view, beside the chat), its project, and
 * Restore for an archived one. See lib/cloud/sessionMenu.
 */
export type CloudConversationMenuAction = "agent_terminal" | "project" | "restore" | "switch_chat";

export type ConversationMenuSection = {
	// Switching to Terminal UI and Archive act on the session, not the
	// conversation, so they sit in a section of their own, Archive last. The internal action id remains `delete` for compatibility with
	// the worker row's actions, but the operation is restorable.
	title: "Open" | "Worker" | "Context" | "Session";
	actions: ConversationMenuAction[];
};

export function conversationMenuSections({ canPin, canCompact, canDelete, canSwitchToTerminal, artifactCount, hasPullRequest }: Pick<ConversationMenuState, "canPin" | "canCompact" | "canDelete" | "canSwitchToTerminal" | "artifactCount" | "hasPullRequest">): ConversationMenuSection[] {
	const worker: ConversationMenuAction[] = [...(canPin ? ["pin" as const] : []), ...(hasPullRequest ? ["pull_requests" as const] : [])];
	const session: ConversationMenuAction[] = [...(canSwitchToTerminal ? ["switch_tui" as const] : []), ...(canDelete ? ["delete" as const] : [])];
	return [
		{ title: "Open", actions: ["shell", "preview", ...(artifactCount ? ["artifacts" as const] : [])] },
		// A section only when it has something in it: the sheet never shows an empty group.
		...(worker.length ? [{ title: "Worker" as const, actions: worker }] : []),
		...(canCompact ? [{ title: "Context" as const, actions: ["compact" as const] }] : []),
		...(session.length ? [{ title: "Session" as const, actions: session }] : []),
	];
}

/** Everything the actions sheet shows, as plain values, so it can be compared and kept still. */
export type ConversationMenuState = {
	canPin: boolean;
	canCompact: boolean;
	canDelete: boolean;
	/** The agent can carry this conversation on in Terminal UI. */
	canSwitchToTerminal?: boolean;
	/** A switch to Terminal UI is starting or under way. */
	switchingToTerminal?: boolean;
	/** A switch back to Chat is starting or under way (a cloud Terminal UI session). */
	switchingToChat?: boolean;
	/** Something the agent produced is ready to preview. */
	previewReady?: boolean;
	/** Files in the session's artifact directory, when known. */
	artifactCount?: number;
	/** The worker has opened at least one PR. */
	hasPullRequest?: boolean;
	pinned: boolean;
	openingShell: boolean;
	compacting: boolean;
	/** A turn is running or queued, which compaction waits for. */
	turnInFlight: boolean;
	/** How much of the context window the conversation fills, 0–100, when the agent reports it. */
	contextPercent?: number;
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
	/**
	 * The app's own glyph in place of `icon`, on both platforms: the pull
	 * request, which neither symbol set draws as the board's header does.
	 */
	glyph?: "pullRequest";
	/** A ring at the row's end filled to this percentage: the context Compact would free. */
	meter?: number;
};

export type ConversationMenuGroup = { title: ConversationMenuSection["title"]; items: ConversationMenuItem[] };

/**
 * The conversation's actions sheet: every action, grouped as the sections
 * above, with the label, state and glyph it shows right now. An action that is
 * running says so in its label and cannot be chosen again until it finishes.
 */
export function conversationMenuGroups(state: ConversationMenuState): ConversationMenuGroup[] {
	return conversationMenuSections(state).map((section) => ({
		title: section.title,
		items: section.actions.map((action) => conversationMenuItem(action, state)),
	}));
}

export function conversationMenuItem(action: ConversationMenuAction, state: ConversationMenuState): ConversationMenuItem {
	switch (action) {
		case "shell": return { action, label: state.openingShell ? "Opening shell…" : "Shell", disabled: state.openingShell, icon: { ios: "chevron.left.forwardslash.chevron.right", android: "code" } };
		case "preview": return { action, label: "Preview", subtitle: state.previewReady ? "Ready to open" : undefined, icon: { ios: "globe", android: "language" } };
		case "artifacts": return { action, label: "Artifacts", subtitle: `${state.artifactCount} ${state.artifactCount === 1 ? "file" : "files"}`, icon: { ios: "doc.text", android: "description" } };
		case "pin": return { action, label: state.pinned ? "Unpin" : "Pin", icon: state.pinned ? { ios: "pin.slash", android: "keep_off" } : { ios: "pin", android: "keep" } };
		case "pull_requests": return { action, label: "PR Review", icon: { ios: "arrow.triangle.pull", android: "rate_review" }, glyph: "pullRequest" };
		case "compact": return {
			action,
			label: state.compacting ? "Compacting…" : "Compact",
			subtitle: state.contextPercent === undefined ? undefined : `${state.contextPercent}% of context used`,
			disabled: state.turnInFlight || state.compacting,
			icon: { ios: "arrow.down.right.and.arrow.up.left", android: "compress" },
			meter: state.contextPercent,
		};
		case "switch_tui": return {
			action,
			label: state.switchingToTerminal ? "Switching to Terminal UI…" : "Switch to Terminal UI",
			subtitle: "Same session, worktree and conversation",
			disabled: state.switchingToTerminal,
			icon: { ios: "terminal", android: "terminal" },
		};
		case "delete": return { action, label: "Archive", subtitle: ARCHIVE_SESSION_HINT, destructive: true, icon: { ios: "archivebox", android: "archive" } };
		case "agent_terminal": return { action, label: "Agent terminal", subtitle: "The agent's own Terminal UI", icon: { ios: "terminal", android: "terminal" } };
		case "project": return { action, label: "Project", icon: { ios: "folder", android: "folder" } };
		case "restore": return { action, label: "Restore", subtitle: "Start its sandbox again where it left off", icon: { ios: "arrow.uturn.backward", android: "history" } };
		case "switch_chat": return {
			action,
			label: state.switchingToChat ? "Switching to Chat…" : "Switch to Chat",
			subtitle: "Same session, worktree and conversation",
			disabled: state.switchingToChat,
			icon: { ios: "bubble.left", android: "chat_bubble" },
		};
	}
}

export function normalizeConversationTitle(value: string): string | undefined {
	const title = value.trim();
	return title || undefined;
}
