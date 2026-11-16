import AsyncStorage from "@react-native-async-storage/async-storage";
import { AppState } from "react-native";
import { restoreRememberedHeights, serializeRememberedHeights, subscribeRememberedHeights } from "./renderFrameModel";

// Page heights only: no content, no credentials. Plain app storage is fine.
const KEY = "ao.chatRenderHeights.v1";
// A thread reports many heights as it loads; save once it settles.
const SAVE_DELAY_MS = 1000;

let saveTimer: ReturnType<typeof setTimeout> | undefined;

function save(): void {
	clearTimeout(saveTimer);
	saveTimer = undefined;
	void AsyncStorage.setItem(KEY, serializeRememberedHeights()).catch(() => {});
}

/**
 * Remembered render heights outlive the launch, so a page opens at the height
 * it had last time instead of shifting once more. Loaded as the chat code is
 * first imported, well before a conversation opens.
 */
export function startRenderHeightStore(): void {
	void AsyncStorage.getItem(KEY).then(restoreRememberedHeights, () => {});
	subscribeRememberedHeights(() => {
		clearTimeout(saveTimer);
		saveTimer = setTimeout(save, SAVE_DELAY_MS);
	});
	// The OS can end a backgrounded app without warning; save what is pending first.
	AppState.addEventListener("change", (state) => {
		if (state !== "active" && saveTimer !== undefined) save();
	});
}
