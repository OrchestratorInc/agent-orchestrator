export type JumpToLatestProps = {
	/** Scrolls the conversation back to its newest message. */
	onPress(): void;
};

/** What screen readers call the control, on both platforms. */
export const JUMP_TO_LATEST_LABEL = "Jump to latest message";
