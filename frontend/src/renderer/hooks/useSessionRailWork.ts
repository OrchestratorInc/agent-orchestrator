import { useMemo } from "react";
import type { RailWork } from "../lib/activity-rail";
import { sessionRailWork } from "../lib/session-rail-work";
import type { WorkspaceSession } from "../types/workspace";
import { useConversation } from "./useConversation";

/**
 * What the activity rail says the agent was asked and did. Only a local Chat
 * session has it: the app already holds that session's history for the chat
 * itself, so this reads the same cache and asks the daemon for nothing more.
 * A Terminal session's agent keeps its own transcript, and a Cloud session's
 * history arrives another way, so both show the rail without these rows.
 */
export function useSessionRailWork(session: WorkspaceSession, hostId: string | undefined, enabled: boolean): RailWork | undefined {
	const chat = enabled && session.mode === "chat" && !session.cloud;
	const { snapshot } = useConversation(chat ? session.id : undefined, hostId);
	return useMemo(() => (chat ? sessionRailWork(snapshot) : undefined), [chat, snapshot]);
}
