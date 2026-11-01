import { useLocalSearchParams } from "expo-router";
import { useEffect, useState } from "react";
import { ConversationUsageSheet } from "../../lib/chat/ConversationUsageSheet";
import { readChatSheet, releaseChatSheet } from "../../lib/chat/chatSheetRegistry";
import { useSheetEntryPresent } from "../../lib/chat/useSheetEntryPresent";

export default function ConversationUsageRoute() {
	const { sheetKey } = useLocalSearchParams<{ sheetKey?: string }>();
	const entry = readChatSheet(sheetKey);
	const usageEntry = entry?.kind === "conversation-usage" ? entry : undefined;
	const [snapshot, setSnapshot] = useState(usageEntry?.snapshot);
	// The numbers keep moving while a turn runs, so the sheet follows the screen's snapshot.
	useEffect(() => usageEntry?.subscribeSnapshot(setSnapshot), [usageEntry]);
	useEffect(() => () => releaseChatSheet(sheetKey), [sheetKey]);
	// Dismiss rather than draw an empty sheet when the hand-off is gone.
	useSheetEntryPresent(Boolean(usageEntry));
	if (!usageEntry) return null;
	return <ConversationUsageSheet sessionTitle={usageEntry.sessionTitle} snapshot={snapshot ?? usageEntry.snapshot} />;
}

export { SheetErrorBoundary as ErrorBoundary } from "../../lib/RouteErrorBoundary";
