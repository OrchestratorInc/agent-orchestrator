import { Alert } from "react-native";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";
import { answerDialog, askTitle, pickFiles, pickerFor, type Pickers } from "./browserAsk";
import { browserAskCopy as copy } from "./BrowserAskSheet.types";

type Answer = (command: BrowserCommand | null) => void;
type PageDialog = Extract<BrowserDialog, { kind: "alert" | "confirm" | "beforeunload" }>;
type FileChooser = Extract<BrowserDialog, { kind: "filechooser" }>;

/** An alert, a confirm or a leave-page question as the system's own alert. */
export function presentPageAlert(dialog: PageDialog, answer: Answer): void {
	if (dialog.kind === "alert") {
		Alert.alert(askTitle(dialog), dialog.message, [{ text: copy.ok, onPress: () => answer(answerDialog(true)) }], { cancelable: false });
		return;
	}
	if (dialog.kind === "beforeunload") {
		Alert.alert(askTitle(dialog), copy.leaveMessage, [
			{ text: copy.stay, style: "cancel", onPress: () => answer(answerDialog(false)) },
			{ text: copy.leave, style: "destructive", onPress: () => answer(answerDialog(true)) },
		], { cancelable: false });
		return;
	}
	Alert.alert(askTitle(dialog), dialog.message, [
		{ text: copy.cancel, style: "cancel", onPress: () => answer(answerDialog(false)) },
		{ text: copy.ok, isPreferred: true, onPress: () => answer(answerDialog(true)) },
	], { cancelable: false });
}

/** The sources a file input can take files from, in the order offered. */
export function fileSources(dialog: FileChooser): Array<"photos" | "files"> {
	return pickerFor(dialog.accept).photos ? ["photos", "files"] : ["files"];
}

/** Picks from one source and answers with what was picked: nothing cancels the input. */
export async function answerWithFiles(source: "photos" | "files", dialog: FileChooser, answer: Answer, pickers?: Pickers): Promise<void> {
	try {
		const files = await pickFiles(source, dialog, pickers);
		answer({ type: "files", payload: { files, folder: dialog.folder } });
	} catch {
		answer({ type: "files", payload: { files: [], folder: dialog.folder } });
		Alert.alert(copy.pickFailed);
	}
}
