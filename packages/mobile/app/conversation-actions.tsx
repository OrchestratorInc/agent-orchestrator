import { useLocalSearchParams, useRouter } from "expo-router";
import { useEffect, useState } from "react";
import { backOr } from "../lib/backNavigation";
import { ConversationActionsPage } from "../lib/chat/ConversationActionsPage";
import { readChatSheet, releaseChatSheet } from "../lib/chat/chatSheetRegistry";
import { useSheetEntryPresent } from "../lib/chat/useSheetEntryPresent";

/**
 * The conversation's "⋯": its actions as a bottom sheet over the chat. Choosing
 * one closes the sheet first and runs the action in the chat, so what it opens (a
 * sheet, a prompt, another page) opens over the conversation it acts on.
 */
export default function ConversationActionsRoute() {
	const router = useRouter();
	const { sheetKey } = useLocalSearchParams<{ sheetKey?: string }>();
	const entry = readChatSheet(sheetKey);
	const actions = entry?.kind === "conversation-actions" ? entry : undefined;
	const [groups, setGroups] = useState(actions?.groups);
	useEffect(() => actions?.subscribeGroups(setGroups), [actions]);
	useEffect(() => () => releaseChatSheet(sheetKey), [sheetKey]);
	// Go back rather than draw an empty page when the hand-off is gone.
	useSheetEntryPresent(Boolean(actions));
	if (!actions) return null;
	return (
		<ConversationActionsPage
			groups={groups ?? actions.groups}
			onSelect={(action) => {
				backOr(router);
				actions.onSelect(action);
			}}
		/>
	);
}

export { RouteErrorBoundary as ErrorBoundary } from "../lib/RouteErrorBoundary";
