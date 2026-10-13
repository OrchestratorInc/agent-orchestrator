import type { DashboardSession, ProjectInfo, SpawnOptions } from "../api";
import type { ConversationPage, SendMessageInput, SendMessageResult } from "../chat/api";
import type { ConversationEvent } from "../chat/sse";
import type { ChatModel, TurnSettings } from "../chat/types";
import type { RequestOptions, StartSessionInterfaceTransitionInput, SessionInterfaceTransition, WorkerOKResponse } from "@aoagents/cloud-client";
import type { CloudInterfaceStatus } from "../cloud/interfaceTransition";

/** Which environment a source speaks for. */
export type EnvironmentKind = "local" | "cloud";

/**
 * The data operations every environment implements.
 *
 * Screens and the store depend on this rather than on daemon functions, so a
 * second environment does not mean threading a second config type through
 * every call site. Deliberately scoped to pass 1: terminal, workspace files,
 * and PRs are not here yet.
 */
export interface SessionSource {
	readonly kind: EnvironmentKind;
	/** Deployment/account/org identity; never a daemon credential. */
	readonly scopeKey?: string;
	listProjects(): Promise<ProjectInfo[]>;
	listSessions(): Promise<DashboardSession[]>;
	createSession(options: SpawnOptions): Promise<{ id: string }>;
	deleteSession(id: string): Promise<void>;
	getConversationPage(id: string, beforeSequence?: number, options?: RequestOptions): Promise<ConversationPage>;
	sendMessage(id: string, input: SendMessageInput): Promise<SendMessageResult>;
	steerMessage?(id: string, input: SendMessageInput, targetTurnId: string): Promise<SendMessageResult>;
	cancelTurn(id: string, turnId: string): Promise<void>;
	subscribeEvents(id: string, listener: (event: ConversationEvent) => void, options?: { after?: number; onError?: (error: unknown) => void }): () => void;
	touchPresence?(id: string, options?: RequestOptions): Promise<void>;
	/**
	 * Wake a paused sandbox. Only meaningful for cloud sessions (see
	 * `isResumable` in `../cloud/lifecycle`) — a local session has no sandbox
	 * lifecycle of its own, so the local source's implementation is a rejection
	 * rather than a silent no-op: nothing should ever call this for a local
	 * session, and a throw makes a caller that does so find out immediately.
	 */
	resumeSession(id: string): Promise<void>;
	retryStartup?(id: string): Promise<void>;
	restoreSession?(id: string): Promise<void>;
	/** Switch a Cloud Terminal session to its structured Chat controller. */
	switchToChat?(id: string, options?: RequestOptions): Promise<void>;
	getInterfaceTransition?(id: string, options?: RequestOptions): Promise<CloudInterfaceStatus>;
	startInterfaceTransition?(id: string, input: StartSessionInterfaceTransitionInput, options?: RequestOptions): Promise<{ transition: SessionInterfaceTransition }>;
	cancelInterfaceTransition?(id: string, options?: RequestOptions): Promise<WorkerOKResponse>;
	acknowledgeInterfaceNotice?(id: string, transitionId: string, options?: RequestOptions): Promise<WorkerOKResponse>;
	/** Answer a Cloud Chat approval shown in the conversation timeline. */
	decideApproval?(id: string, requestId: string, decisionId: string): Promise<void>;
	getChatModels?(id: string): Promise<ChatModel[]>;
	setTurnSettings?(id: string, settings: TurnSettings): Promise<void>;
}
