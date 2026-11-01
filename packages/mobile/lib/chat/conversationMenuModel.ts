import type { IOSSymbol } from "../symbols";
import type { MaterialDrawableName } from "../toolbarIcon.types";
import { ARCHIVE_SESSION_HINT } from "../worker-action-model";

/**
 * What the conversation's actions page offers, and nothing more: the
 * worktree's terminal, the preview and the artifacts, pinning the worker and reviewing its
 * PR, compacting the history (its context use drawn as a ring), and archiving
 * the session.
 */
export type ConversationMenuAction = "shell" | "preview" | "artifacts" | "pin" | "pull_requests" | "compact" | "delete";

export type ConversationMenuSection = {
	// Archive acts on the session, not the conversation, so it sits in a section
	// of its own. The internal action id remains `delete` for compatibility with
	// the worker row's actions, but the operation is restorable.
	title: "Open" | "Worker" | "Context" | "Session";
	actions: ConversationMenuAction[];
};

export function conversationMenuSections({ canPin, canCompact, canDelete }: { canPin: boolean; canCompact: boolean; canDelete: boolean }): ConversationMenuSection[] {
	return [
		{ title: "Open", actions: ["shell", "preview", "artifacts"] },
		{ title: "Worker", actions: [...(canPin ? ["pin" as const] : []), "pull_requests"] },
		// A section only when it has something in it: no page shows an empty group.
		...(canCompact ? [{ title: "Context" as const, actions: ["compact" as const] }] : []),
		...(canDelete ? [{ title: "Session" as const, actions: ["delete" as const] }] : []),
	];
}

/** Everything the actions page shows, as plain values, so it can be compared and kept still. */
export type ConversationMenuState = {
	canPin: boolean;
	canCompact: boolean;
	canDelete: boolean;
	/** Something the agent produced is ready to preview. */
	previewReady?: boolean;
	/** Files in the session's artifact directory, when known. */
	artifactCount?: number;
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
	/** A ring at the row's end filled to this percentage: the context Compact would free. */
	meter?: number;
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
		case "shell": return { action, label: state.openingShell ? "Opening terminal…" : "Terminal", disabled: state.openingShell, icon: { ios: "terminal", android: "terminal" } };
		case "preview": return { action, label: "Preview", subtitle: state.previewReady ? "Ready to open" : undefined, icon: { ios: "globe", android: "language" } };
		case "artifacts": return { action, label: "Artifacts", subtitle: state.artifactCount ? `${state.artifactCount} ${state.artifactCount === 1 ? "file" : "files"}` : undefined, icon: { ios: "doc.text", android: "description" } };
		case "pin": return { action, label: state.pinned ? "Unpin" : "Pin", icon: state.pinned ? { ios: "pin.slash", android: "keep_off" } : { ios: "pin", android: "keep" } };
		case "pull_requests": return { action, label: "PR Review", icon: { ios: "arrow.triangle.pull", android: "rate_review" } };
		case "compact": return {
			action,
			label: state.compacting ? "Compacting…" : "Compact",
			subtitle: state.contextPercent === undefined ? undefined : `${state.contextPercent}% of context used`,
			disabled: state.turnInFlight || state.compacting,
			icon: { ios: "arrow.down.right.and.arrow.up.left", android: "compress" },
			meter: state.contextPercent,
		};
		case "delete": return { action, label: "Archive", subtitle: ARCHIVE_SESSION_HINT, destructive: true, icon: { ios: "archivebox", android: "archive" } };
	}
}

export function normalizeConversationTitle(value: string): string | undefined {
	const title = value.trim();
	return title || undefined;
}
