import { useCallback, useEffect, useState } from "react";
import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";

/**
 * Side chats are a Developer mode experiment for local desktop chat sessions
 * whose harness the daemon supports. This keeps existing sides accessible;
 * creation and AO slash commands also require the live resume capability.
 */
export function useSideChatsAvailable(session: WorkspaceSession | undefined, hostId?: string): boolean {
	const enabled = useUiStore((state) => state.developerMode && state.sideChats);
	// Keep existing side chats accessible while the parent controller hibernates.
	return Boolean(
		enabled &&
			session &&
			!hostId &&
			session.mode === "chat" &&
			!session.cloud &&
			!session.isTerminated,
	);
}

/** What the chat surface knows about a session's side chats once their list has loaded. */
export type SideChatsState = { present: boolean; creating: boolean };

/**
 * The inspector's Side chat tab shows only while the session has a side chat,
 * or while it is selected so a new side chat can open in it. With none left
 * and none being created, a selected Side chat tab falls back to Summary.
 */
export function useSideChatTab(uiSessionId: string, available: boolean) {
	const view = useUiStore((state) => state.inspectorSessions[uiSessionId]?.view ?? "summary");
	const setInspectorView = useUiStore((state) => state.setInspectorView);
	const [reported, setReported] = useState<SideChatsState & { key: string }>();
	const current = reported?.key === uiSessionId ? reported : undefined;
	const onSideChatsChange = useCallback((next: SideChatsState) => setReported({ ...next, key: uiSessionId }), [uiSessionId]);
	const present = current?.present;
	const creating = current?.creating ?? false;
	useEffect(() => {
		if (present !== false || creating) return;
		if (useUiStore.getState().inspectorSessions[uiSessionId]?.view === "sideChat") setInspectorView(uiSessionId, "summary");
	}, [creating, present, setInspectorView, uiSessionId]);
	return { showTab: available && (present === true || view === "sideChat"), onSideChatsChange };
}
