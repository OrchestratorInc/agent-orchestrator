import { useEffect } from "react";
import { ToastHost } from "../Toast";
import { useToast } from "../useToast";
import type { TerminalNoticeHostProps, TerminalNoticeProps } from "./TerminalNotice.types";

export type { TerminalNoticeHostProps, TerminalNoticeMessage, TerminalNoticeProps, TerminalNoticeTone } from "./TerminalNotice.types";

/** Android draws no inline notice: `TerminalNoticeHost` shows it as a snackbar. */
export function TerminalNotice(_props: TerminalNoticeProps): null {
	return null;
}

/**
 * A terminal notice on Android: Material 3's snackbar above the dock, with the
 * system's timing and TalkBack announcement. A failure stays up long enough to
 * read and can be closed sooner; news is brief.
 */
export function TerminalNoticeHost({ notice, onDismiss, bottomOffset }: TerminalNoticeHostProps) {
	const toast = useToast();
	const { show } = toast;

	useEffect(() => {
		if (!notice) return;
		show(notice.text, notice.tone === "error" ? { duration: "long", tone: "warning", dismissible: true } : { duration: "short" });
		// The snackbar holds it from here. Clearing the screen's copy lets the same
		// words come back later as a new notice.
		onDismiss();
	}, [notice, onDismiss, show]);

	return <ToastHost toast={toast} bottomOffset={bottomOffset} />;
}
