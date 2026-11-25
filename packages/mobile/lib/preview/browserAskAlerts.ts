import { Alert } from "react-native";
import type { BrowserCommand, BrowserDialog } from "../browserTransport";
import { pickFiles, pickerFor, type Pickers } from "./browserAsk";
import { browserAskCopy as copy } from "./BrowserAskSheet.types";

type Answer = (command: BrowserCommand | null) => void;
type FileChooser = Extract<BrowserDialog, { kind: "filechooser" }>;

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
