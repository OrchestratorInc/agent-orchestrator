import type { SessionProvisionStep } from "../../types/workspace";
import { ShellTopbar } from "../ShellTopbar";
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

export function OrchestratorStartingChat({ steps }: { steps?: readonly SessionProvisionStep[] }) {
	return <ChatWorkspace
		snapshot={pendingSnapshot}
		sessionRole="orchestrator"
		startingSteps={steps}
		newWorkDisabled
		inlineHeader
		headerActions={
			<div className="session-topbar-session-chrome flex shrink-0 items-center" data-compact-session-chrome="false">
				<ShellTopbar embedded startingOrchestrator />
			</div>
		}
	/>;
}
