import { useEffect, useRef } from "react";
import { Alert } from "react-native";
import { userFacingError } from "../connectionError";
import { haptics } from "../haptics";
import { normalizeConversationTitle } from "./conversationMenuModel";
import {
	CONVERSATION_TITLE_MAX_LENGTH,
	RENAME_FAILED,
	RENAME_PROMPT_MESSAGE,
	RENAME_PROMPT_TITLE,
	type ConversationRenamePromptProps,
	type ConversationRenameRequest,
} from "./ConversationRenamePrompt.types";

export type { ConversationRenamePromptProps, ConversationRenameRequest } from "./ConversationRenamePrompt.types";

/**
 * Renaming as the system's text prompt: a UIAlertController with one field,
 * Cancel and Save. Android's is a Material dialog (./ConversationRenamePrompt.android).
 *
 * A component, though it draws nothing here, so both platforms take the same
 * request from the screen.
 */
export function ConversationRenamePrompt({ request, onClose }: ConversationRenamePromptProps): null {
	const close = useRef(onClose);
	close.current = onClose;
	useEffect(() => {
		// Once per request: the screen can remount this while the alert is up.
		if (!request || asked.has(request)) return;
		asked.add(request);
		askForTitle(request, request.initialTitle, () => close.current());
	}, [request]);
	return null;
}

const asked = new WeakSet<ConversationRenameRequest>();

function askForTitle(request: ConversationRenameRequest, draft: string, done: () => void) {
	Alert.prompt(
		RENAME_PROMPT_TITLE,
		RENAME_PROMPT_MESSAGE,
		[
			{ text: "Cancel", style: "cancel", onPress: done },
			{ text: "Save", isPreferred: true, onPress: (value?: string) => void save(request, value ?? "", done) },
		],
		"plain-text",
		draft,
	);
}

async function save(request: ConversationRenameRequest, value: string, done: () => void) {
	const title = normalizeConversationTitle(value.slice(0, CONVERSATION_TITLE_MAX_LENGTH));
	// Nothing to rename to: the same as Cancel.
	if (!title) return done();
	try {
		await request.onRename(title);
		haptics.success();
		done();
	} catch (cause) {
		haptics.error();
		// The alert has closed by now, so the reason comes in one of its own, with
		// the way back to what was typed.
		Alert.alert(RENAME_FAILED, userFacingError(cause, RENAME_FAILED), [
			{ text: "Cancel", style: "cancel", onPress: done },
			{ text: "Try again", isPreferred: true, onPress: () => askForTitle(request, title, done) },
		]);
	}
}
