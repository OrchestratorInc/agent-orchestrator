import type { IOSSymbol } from "../symbols";

export type TerminalMenuAction = "switch_chat" | "preview" | "files" | "artifacts" | "zoom_out" | "zoom_in" | "fit_width" | "archive" | "close_shell" | "stop_reviewer";

/** Everything the terminal's "⋯" shows, as plain values, so the header is rebuilt only when one changes. */
export type TerminalMenuState = {
	/** An agent's session rather than a worktree shell or a reviewer: it has Chat, a preview and files. */
	agentSession: boolean;
	/** The daemon says Chat can resume this session. */
	chatReady: boolean;
	/** Asking the daemon again whether Chat can open. */
	checkingChat: boolean;
	/** A switch to Chat is starting or under way. */
	switchingToChat: boolean;
	/** Something the agent produced is ready to preview. */
	previewReady?: boolean;
	/** Files in the session's artifact directory. */
	artifactCount: number;
	/** The terminal is live, so its view can zoom. */
	canZoom: boolean;
	/** What closing this terminal means, when it can be closed. */
	close?: "archive" | "shell" | "reviewer";
};

export type TerminalMenuItem = {
	action: TerminalMenuAction;
	label: string;
	/** A second line under the label, where the platform's menu draws one. */
	subtitle?: string;
	disabled?: boolean;
	destructive?: boolean;
	/** The iOS glyph; an M3 menu row has none. */
	icon: IOSSymbol;
};

export type TerminalMenuSection = {
	/** The view's three steps are one row of glyphs on iOS (a palette), not three rows. */
	kind: "session" | "view" | "close";
	items: TerminalMenuItem[];
};

/**
 * The terminal's "⋯", the only item in its bar: the switch to Chat and what
 * the session opens, then the view (zoom changes only this phone's view, never
 * the grid the desktop shares), then closing it. An empty section is left out.
 */
export function terminalMenuSections(state: TerminalMenuState): TerminalMenuSection[] {
	const session: TerminalMenuItem[] = state.agentSession
		? [
			switchToChatItem(state),
			{ action: "preview", label: "Preview", subtitle: state.previewReady ? "Ready to open" : undefined, icon: state.previewReady ? "globe.americas.fill" : "globe" },
			{ action: "files", label: "Files", icon: "doc.text.magnifyingglass" },
			...(state.artifactCount ? [{ action: "artifacts" as const, label: `Artifacts (${state.artifactCount})`, icon: "doc.text" as const }] : []),
		]
		: [];
	const view: TerminalMenuItem[] = state.canZoom
		? [
			{ action: "zoom_out", label: "Smaller", icon: "textformat.size.smaller" },
			{ action: "zoom_in", label: "Larger", icon: "textformat.size.larger" },
			{ action: "fit_width", label: "Fit width", icon: "arrow.left.and.right" },
		]
		: [];
	const close: TerminalMenuItem[] = state.close ? [closeItem(state.close)] : [];
	const sections: TerminalMenuSection[] = [{ kind: "session", items: session }, { kind: "view", items: view }, { kind: "close", items: close }];
	return sections.filter((section) => section.items.length > 0);
}

function switchToChatItem({ chatReady, checkingChat, switchingToChat }: TerminalMenuState): TerminalMenuItem {
	return {
		action: "switch_chat",
		label: switchingToChat ? "Switching to Chat…" : checkingChat ? "Checking Chat…" : "Switch to Chat",
		// Still offered before the daemon says yes: choosing it asks again and
		// explains what is missing.
		subtitle: chatReady ? undefined : "Not ready yet",
		disabled: switchingToChat || checkingChat,
		icon: "bubble.left",
	};
}

function closeItem(close: NonNullable<TerminalMenuState["close"]>): TerminalMenuItem {
	if (close === "archive") return { action: "archive", label: "Archive session", destructive: true, icon: "archivebox" };
	if (close === "shell") return { action: "close_shell", label: "Close shell", destructive: true, icon: "xmark" };
	return { action: "stop_reviewer", label: "Stop reviewer", destructive: true, icon: "stop.circle" };
}
