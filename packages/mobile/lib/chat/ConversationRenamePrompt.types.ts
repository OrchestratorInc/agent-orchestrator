export type ConversationRenameRequest = {
	/** Tells one request from the next, so asking again opens a fresh prompt. */
	id: number;
	initialTitle: string;
	/** Rejects to keep the prompt up with the reason. */
	onRename(title: string): Promise<void> | void;
};

export type ConversationRenamePromptProps = {
	/** The rename being asked for; nothing shows without one. */
	request: ConversationRenameRequest | undefined;
	/** The prompt is gone: renamed, cancelled or dismissed. */
	onClose(): void;
};

export const RENAME_PROMPT_TITLE = "Rename conversation";
export const RENAME_PROMPT_MESSAGE = "Use a short name that makes this worker easy to find.";
export const RENAME_FAILED = "Couldn't rename this conversation.";
/** The longest title the prompt keeps. */
export const CONVERSATION_TITLE_MAX_LENGTH = 100;
