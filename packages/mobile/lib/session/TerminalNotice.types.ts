/** Whether a notice reports a failure or only says what happened. */
export type TerminalNoticeTone = "error" | "info";

export type TerminalNoticeMessage = {
	/** New for every notice, so the same words raised twice still show twice. */
	id: number;
	text: string;
	tone: TerminalNoticeTone;
};

export type TerminalNoticeProps = {
	notice: TerminalNoticeMessage | null;
	/** Takes the notice down. */
	onDismiss: () => void;
};

export type TerminalNoticeHostProps = TerminalNoticeProps & {
	/** Distance from the bottom of the screen: the dock and anything under it. */
	bottomOffset: number;
};
