import AsyncStorage from "@react-native-async-storage/async-storage";
import { forgetMachineComposerAttachments } from "../chat/composerAttachments";
import { CLOUD_HOST_ID } from "./mapping";

// The composer keeps a session's unsent text on disk and its picked files in
// memory, keyed by machine and session (ChatComposer). For Orchestrator Cloud
// both belong to the account that typed them: another account opening the same
// session on this phone must not find them.
const DRAFT_PREFIX = `ao.chat.draft.${CLOUD_HOST_ID}.`;

export async function forgetCloudDrafts(): Promise<void> {
	forgetMachineComposerAttachments(CLOUD_HOST_ID);
	const keys = (await AsyncStorage.getAllKeys()).filter((key) => key.startsWith(DRAFT_PREFIX));
	if (keys.length) await AsyncStorage.multiRemove(keys);
}
