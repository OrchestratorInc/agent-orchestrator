import type { BrowserCommand, BrowserDialog } from "../browserTransport";
import type { Pickers } from "./browserAsk";

export type BrowserAskSheetProps = {
	/** What the page asks now, or null. A new one replaces the last. */
	dialog: BrowserDialog | null;
	/**
	 * The person's answer to `dialog`, or null when they gave none the host
	 * needs (a sheet dismissed). Called once per dialog.
	 */
	onAnswer(dialog: BrowserDialog, command: BrowserCommand | null): void;
	/** Tests swap the system pickers. */
	pickers?: Pickers;
};

export const browserAskCopy = {
	ok: "OK",
	cancel: "Cancel",
	stay: "Stay",
	leave: "Leave",
	leaveMessage: "Changes you made may not be saved.",
	signIn: "Sign in",
	username: "Username",
	password: "Password",
	photos: "Photo Library",
	files: (multiple: boolean) => (multiple ? "Choose Files" : "Choose File"),
	pickFailed: "Couldn't open the picker.",
} as const;
