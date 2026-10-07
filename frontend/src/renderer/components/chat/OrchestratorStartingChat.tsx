import type { ConversationSnapshot } from "../../types/conversation";
import { ChatWorkspace } from "./ChatWorkspace";

export function startingConversationSnapshot(sessionId: string, harness: ConversationSnapshot["harness"]): ConversationSnapshot {
	return {
		conversationId: sessionId, sessionId, harness, mode: "chat",
		controller: { state: "connecting" }, latestSequence: 0, oldestSequence: 0,
		hasMoreBefore: false, activeBranchId: "branch-root", branchPoints: [],
		settings: {}, mcpServers: [], capabilities: [], turns: [], items: [],
	};
}

const pendingSnapshot = startingConversationSnapshot("pending-orchestrator", "claude-code");

/** The normal chat surface, shown before project/session creation returns. */
export function OrchestratorStartingChat() {
	return <ChatWorkspace snapshot={pendingSnapshot} sessionRole="orchestrator" starting newWorkDisabled />;
}
