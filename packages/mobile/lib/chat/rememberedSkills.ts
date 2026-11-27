import type { ChatSkill } from "./types";

// The "/" list each agent's chats last reported, per machine, so a new task's
// list starts from the same skills and built-in commands (`/compact`) the
// chat's does, as a new chat on the desktop does. A project's chat is kept
// under that project only, since its list holds the project's own skills; the
// machine-wide list for the agent comes from chats with no project.
const REMEMBERED_SKILL_LISTS = 32;
const lists = new Map<string, ChatSkill[]>();

const keyFor = (hostId: string, harness: string, projectId?: string) => `${hostId}|${harness}|${projectId ?? ""}`;

function remember(key: string, skills: ChatSkill[]) {
	// Insertion order is recency, so the list least used is the one dropped.
	lists.delete(key);
	lists.set(key, skills);
	while (lists.size > REMEMBERED_SKILL_LISTS) {
		const oldest = lists.keys().next().value;
		if (oldest === undefined) break;
		lists.delete(oldest);
	}
}

export function rememberChatSkills(hostId: string | undefined, harness: string | undefined, projectId: string | undefined, skills: ChatSkill[]): void {
	if (!hostId || !harness || !skills.length) return;
	remember(keyFor(hostId, harness, projectId), skills);
}

/**
 * The project's own last list, else the last a chat of this agent with no
 * project reported on the machine: never another project's.
 */
export function rememberedChatSkills(hostId: string, harness: string, projectId?: string): ChatSkill[] {
	return (projectId ? lists.get(keyFor(hostId, harness, projectId)) : undefined) ?? lists.get(keyFor(hostId, harness)) ?? [];
}

/** Drops every remembered list when a machine is forgotten, as the remembered model catalogs go. */
export function forgetRememberedChatSkills(): void {
	lists.clear();
}

/**
 * The list a chat reported keeps its order, so the menu does not reshuffle
 * between the new task and the chat it becomes; skills found on disk since, or
 * never reported because no chat has run, follow it.
 */
export function mergeChatSkills(reported: ChatSkill[], scanned: ChatSkill[]): ChatSkill[] {
	if (scanned.length === 0) return reported;
	if (reported.length === 0) return scanned;
	const names = new Set(reported.map((skill) => skill.name));
	const added = scanned.filter((skill) => !names.has(skill.name));
	return added.length === 0 ? reported : [...reported, ...added];
}
