import { getAgentSkills } from "./api";
import { createAsyncValueCache } from "./chat/asyncValueCache";
import { mergeChatSkills, rememberedChatSkills } from "./chat/rememberedSkills";
import type { ChatSkill } from "./chat/types";
import type { ServerConfig } from "./config";
import { UnreachableError } from "./connectionError";

// Skill folders change only when someone edits them; a `/` typed again soon
// after, or on the page opened again, reuses the list rather than rescanning.
const cache = createAsyncValueCache<string, ChatSkill[]>(16, 30_000);

/**
 * The skills a new task's agent offers before it starts, as its chats list
 * them: what a chat of this agent last reported first (built-in commands
 * included), then what the machine finds in the agent's own folders, the
 * project's before the user's. Without a project (a standalone agent) only the
 * user's are scanned.
 */
export async function loadSpawnSkills(config: ServerConfig | null, hostId: string, agent: string, projectId?: string): Promise<ChatSkill[]> {
	const reported = rememberedChatSkills(hostId, agent, projectId);
	try {
		if (!config) throw new UnreachableError("offline");
		const scanned = await cache.load(`${hostId}|${agent}|${projectId ?? ""}`, () => getAgentSkills(config, agent, projectId));
		return mergeChatSkills(reported, scanned);
	} catch (cause) {
		if (reported.length) return reported;
		throw cause;
	}
}
