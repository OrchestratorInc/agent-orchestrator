import { useEffect, useRef } from "react";
import { ActionSheetIOS, Alert } from "react-native";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";
import { answerAuth, answerDialog, answerSelect, askTitle, authMessage } from "./browserAsk";
import { browserAskCopy as copy, type BrowserAskSheetProps } from "./BrowserAskSheet.types";
import { answerWithFiles, fileSources, presentPageAlert } from "./browserAskAlerts";

export type { BrowserAskSheetProps } from "./BrowserAskSheet.types";

/**
 * What the page asks, as the system's own UI: alerts and prompts, a sign-in
 * prompt with a secure password field, and action sheets for a select and
 * for where to pick files from. It draws nothing in the screen itself.
 */
export function BrowserAskSheet({ dialog, onAnswer, pickers }: BrowserAskSheetProps) {
	// A sheet outlives the render that showed it, so it answers through the latest handler.
	const latest = useRef({ onAnswer, pickers });
	latest.current = { onAnswer, pickers };

	useEffect(() => {
		if (!dialog) return;
		let answered = false;
		const answer = (command: BrowserCommand | null) => {
			if (answered) return;
			answered = true;
			latest.current.onAnswer(dialog, command);
		};
		present(dialog, answer, () => latest.current.pickers);
	}, [dialog]);

	return null;
}

function present(dialog: BrowserDialog, answer: (command: BrowserCommand | null) => void, pickers: () => BrowserAskSheetProps["pickers"]): void {
	switch (dialog.kind) {
		case "alert":
		case "confirm":
		case "beforeunload":
			presentPageAlert(dialog, answer);
			return;
		case "prompt":
			Alert.prompt(askTitle(dialog), dialog.message, [
				{ text: copy.cancel, style: "cancel", onPress: () => answer(answerDialog(false)) },
				{ text: copy.ok, isPreferred: true, onPress: (value?: string) => answer(answerDialog(true, value ?? "")) },
			], "plain-text", dialog.defaultPrompt ?? "");
			return;
		case "auth":
			// The system's login prompt: its password field is secure, and what is
			// typed goes to the host only.
			Alert.prompt(askTitle(dialog), authMessage(dialog), [
				{ text: copy.cancel, style: "cancel", onPress: () => answer(answerAuth(dialog, null)) },
				{
					text: copy.signIn,
					isPreferred: true,
					onPress: (value?: string | { login: string; password: string }) => {
						const typed = typeof value === "object" && value ? value : { login: "", password: "" };
						answer(answerAuth(dialog, { username: typed.login, password: typed.password }));
					},
				},
			], "login-password");
			return;
		case "select": {
			const cancel = dialog.options.length;
			ActionSheetIOS.showActionSheetWithOptions({
				title: askTitle(dialog),
				options: [...dialog.options.map((option, index) => (index === dialog.selected ? `✓ ${option.label}` : option.label)), copy.cancel],
				cancelButtonIndex: cancel,
				disabledButtonIndices: dialog.options.flatMap((option, index) => (option.disabled ? [index] : [])),
			}, (index) => answer(index === cancel ? null : answerSelect(dialog, index)));
			return;
		}
		case "filechooser": {
			const sources = fileSources(dialog);
			const cancel = sources.length;
			ActionSheetIOS.showActionSheetWithOptions({
				title: askTitle(dialog),
				options: [...sources.map((source) => (source === "photos" ? copy.photos : copy.files(dialog.multiple))), copy.cancel],
				cancelButtonIndex: cancel,
			}, (index) => {
				const source = sources[index];
				if (!source) answer({ type: "files", payload: { files: [], folder: dialog.folder } });
				else void answerWithFiles(source, dialog, answer, pickers());
			});
			return;
		}
	}
}
