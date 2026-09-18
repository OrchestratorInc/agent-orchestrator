import type { DashboardSession, ProjectInfo } from "../api";
import type { SpawnOptions } from "../store";
import type { ConversationPage, SendMessageInput, SendMessageResult } from "../chat/api";
import type { ConversationEvent } from "../chat/api";

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
	listProjects(): Promise<ProjectInfo[]>;
	listSessions(): Promise<DashboardSession[]>;
	createSession(options: SpawnOptions): Promise<{ id: string }>;
	deleteSession(id: string): Promise<void>;
	getConversationPage(id: string, beforeSequence?: number): Promise<ConversationPage>;
	sendMessage(id: string, input: SendMessageInput): Promise<SendMessageResult>;
	cancelTurn(id: string, turnId: string): Promise<void>;
	subscribeEvents(id: string, listener: (event: ConversationEvent) => void): () => void;
}
