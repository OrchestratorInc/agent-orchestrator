import { useQuery } from "@tanstack/react-query";
import { apiClient } from "../lib/api-client";
import { clientForHost } from "../lib/host-clients";
import type { ChatSkill } from "../types/conversation";

const NO_SKILLS: ChatSkill[] = [];

/**
 * The skills an agent offers in a project before any session of it runs, read by
 * the host from the agent's own skill folders. The new chat page's "/" menu uses
 * it so a first chat with an agent can pick a skill; a running session asks its
 * agent instead.
 */
export function useAgentSkillsQuery(agent: string, projectId: string | undefined, hostId: string | undefined, enabled: boolean): ChatSkill[] {
	const query = useQuery({
		queryKey: ["agent-skills", hostId ?? "", agent, projectId ?? ""],
		enabled: enabled && Boolean(agent),
		retry: false,
		// Skill folders change only when the user edits them; a revisit rereads.
		staleTime: 30_000,
		queryFn: async ({ signal }) => {
			const client = hostId ? clientForHost(hostId) : apiClient;
			const { data, error } = await client.GET("/api/v1/agents/{agent}/skills", {
				params: { path: { agent }, query: { projectId: projectId || undefined } },
				signal,
			});
			if (error) throw error;
			return (data?.skills ?? []) as ChatSkill[];
		},
	});
	return query.data ?? NO_SKILLS;
}

/**
 * The list a live session of this agent last reported keeps its order, so the
 * menu does not reshuffle when the chat starts; skills found on disk since, or
 * never reported because no session has run, follow it.
 */
export function mergeChatSkills(reported: ChatSkill[], scanned: ChatSkill[]): ChatSkill[] {
	if (scanned.length === 0) return reported;
	if (reported.length === 0) return scanned;
	const names = new Set(reported.map((skill) => skill.name));
	const added = scanned.filter((skill) => !names.has(skill.name));
	return added.length === 0 ? reported : [...reported, ...added];
}
