export type ConversationMenuAction =
	| "files"
	| "shell"
	| "preview"
	| "pull_requests"
	| "map"
	| "refresh"
	| "settings"
	| "rename"
	| "pin"
	| "compact"
	| "terminal_ui"
	| "reload_mcp"
	| "delete";

export type ConversationMenuSection = {
	// Archive acts on the session, not the conversation, so it sits in a section
	// of its own. The internal action id remains `delete` for compatibility with
	// the existing sheet routing, but the operation is restorable.
	title: "Workspace" | "Conversation" | "Agent" | "Session";
	actions: ConversationMenuAction[];
};

export function conversationMenuSections({
	canOpenFiles,
	canOpenShell,
	canPreview,
	canPullRequests,
	canRename,
	canPin,
	canCompact,
	canReloadMcp,
	canDelete,
}: {
	canOpenFiles: boolean;
	canOpenShell: boolean;
	canPreview: boolean;
	canPullRequests: boolean;
	canRename: boolean;
	canPin: boolean;
	canCompact: boolean;
	canReloadMcp: boolean;
	canDelete: boolean;
}): ConversationMenuSection[] {
	const sections: ConversationMenuSection[] = [
		{
			title: "Workspace",
			actions: [
				...(canOpenFiles ? ["files" as const] : []),
				...(canOpenShell ? ["shell" as const] : []),
				...(canPreview ? ["preview" as const] : []),
				...(canPullRequests ? ["pull_requests" as const] : []),
			],
		},
		{
			title: "Conversation",
			actions: [
				"map",
				"refresh",
				"settings",
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
	return sections.filter((section) => section.actions.length > 0);
}

export function normalizeConversationTitle(value: string): string | undefined {
	const title = value.trim();
	return title || undefined;
}
