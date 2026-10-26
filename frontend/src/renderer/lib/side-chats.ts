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
