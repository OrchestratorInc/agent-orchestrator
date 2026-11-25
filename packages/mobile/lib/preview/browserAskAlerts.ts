import { Alert } from "react-native";
import type { BrowserCommand, BrowserDialog, BrowserPickedFile } from "../browserTransport";
import { pickFiles, pickerFor, type Pickers } from "./browserAsk";
import { browserAskCopy as copy } from "./BrowserAskSheet.types";

type Answer = (command: BrowserCommand | null) => void;
type FileChooser = Extract<BrowserDialog, { kind: "filechooser" }>;

/** The sources a file input can take files from, in the order offered. */
export function fileSources(dialog: FileChooser): Array<"photos" | "files"> {
	return pickerFor(dialog.accept).photos ? ["photos", "files"] : ["files"];
}

/** The answer to a file chooser: these files, for that chooser. None cancels it. */
export function filesAnswer(dialog: FileChooser, files: BrowserPickedFile[]): BrowserCommand {
	return { type: "files", payload: { files, folder: dialog.folder, ...(dialog.id ? { chooser: dialog.id } : {}) } };
}

/** Picks from one source and answers with what was picked: nothing cancels the input. */
export async function answerWithFiles(source: "photos" | "files", dialog: FileChooser, answer: Answer, pickers?: Pickers): Promise<void> {
	try {
		const files = await pickFiles(source, dialog, pickers);
		answer(filesAnswer(dialog, files));
	} catch {
		answer(filesAnswer(dialog, []));
		Alert.alert(copy.pickFailed);
	}
}
