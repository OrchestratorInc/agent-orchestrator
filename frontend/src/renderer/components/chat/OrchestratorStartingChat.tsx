import { useMemo } from "react";
import type { SessionProvisionStep } from "../../types/workspace";
import { SessionTopbarActions } from "../SessionTopbarActions";
import type { ConversationSnapshot } from "../../types/conversation";
import { ChatWorkspace } from "./ChatWorkspace";
import { CloudSessionStartupLoader } from "../CloudSessionStartupLoader";

export function startingConversationSnapshot(sessionId: string, harness: ConversationSnapshot["harness"]): ConversationSnapshot {
	return {
		conversationId: sessionId, sessionId, harness, mode: "chat",
		controller: { state: "connecting" }, latestSequence: 0, oldestSequence: 0,
		hasMoreBefore: false, activeBranchId: "branch-root", branchPoints: [],
		settings: {}, mcpServers: [], capabilities: [], turns: [], items: [],
	};
}

/**
 * The orchestrator's view while it is being created. A Freestyle cloud
 * orchestrator starts in place, on its conversation with the composer's
 * startup shimmer, as a local one does; other cloud providers (Coder) start
 * behind the startup screen, which shows their steps and workspace link.
 */
export function OrchestratorStartingChat({ steps, cloud = false, freestyle = false, coderWorkspaceUrl, harness }: { steps?: readonly SessionProvisionStep[]; cloud?: boolean; freestyle?: boolean; coderWorkspaceUrl?: string; harness?: string }) {
	// The harness being started picks the agent icon; Claude Code only when
	// the project names none and no orchestrator session exists yet.
	const snapshot = useMemo(() => startingConversationSnapshot("pending-orchestrator", harness || "claude-code"), [harness]);
	if (cloud && !freestyle) return <CloudSessionStartupLoader coderWorkspaceUrl={coderWorkspaceUrl} />;
	return <ChatWorkspace
		snapshot={snapshot}
		sessionRole="orchestrator"
		startingSteps={steps}
		newWorkDisabled
		pendingInPlaceStart={cloud && freestyle}
		inlineHeader
		headerActions={
			<div className="session-topbar-session-chrome flex shrink-0 items-center" data-compact-session-chrome="false">
				<SessionTopbarActions startingOrchestrator />
			</div>
		}
	/>;
}
