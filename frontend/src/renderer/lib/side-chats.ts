import { useUiStore } from "../stores/ui-store";
import type { WorkspaceSession } from "../types/workspace";

export function useSideChatsAvailable(session: WorkspaceSession | undefined, hostId?: string): boolean {
	const enabled = useUiStore((state) => state.developerMode && state.sideChats);
	return Boolean(
		enabled &&
			session &&
			!hostId &&
			session.mode === "chat" &&
			!session.cloud &&
			!session.isTerminated,
	);
}
