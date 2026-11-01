import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import type { CloudCpShareCandidate, CloudCpTeam, CloudCpTeamDetail } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";

/** The workspace's teams. */
export function teamsQueryKey(baseUrl: string, orgId: string) {
	return ["cloud-teams", baseUrl, orgId] as const;
}

/** The teams the signed-in person is in, in every workspace. */
export function myTeamsQueryKey(baseUrl: string) {
	return ["cloud-my-teams", baseUrl] as const;
}

/** One team; `external` reads it as a member from another workspace. */
export function teamQueryKey(baseUrl: string, orgId: string, teamId: string, external: boolean) {
	return ["cloud-team", baseUrl, orgId, teamId, external] as const;
}

/** Who has access to a session (sessionId) or a whole project. */
export function shareAccessQueryKey(baseUrl: string, orgId: string, projectId: string, sessionId: string | undefined) {
	return ["cloud-share-access", baseUrl, orgId, projectId, sessionId ?? ""] as const;
}

export function teamShareAccessQueryKey(baseUrl: string, orgId: string, projectId: string, sessionId: string | undefined, teamId: string) {
	return ["cloud-team-share-access", baseUrl, orgId, projectId, sessionId ?? "", teamId] as const;
}

export function shareCandidatesQueryKey(baseUrl: string, orgId: string, query: string) {
	return ["cloud-share-candidates", baseUrl, orgId, query] as const;
}

/**
 * Everything a change to a team or a share can make stale: the teams lists,
 * the team pages, the share dialogs, and the session participants.
 */
export function invalidateTeamsAndShares(queryClient: QueryClient) {
	for (const key of ["cloud-teams", "cloud-my-teams", "cloud-team", "cloud-share-access", "cloud-team-share-access", "cloud-share-candidates", "cloud-session-participants"]) {
		void queryClient.invalidateQueries({ queryKey: [key] });
	}
}

export function useTeams(orgId: string | undefined, enabled = true) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: teamsQueryKey(baseUrl, orgId ?? ""),
		enabled: enabled && ready && Boolean(orgId),
		queryFn: async (): Promise<CloudCpTeam[]> => (await client.listTeams(orgId!)).teams,
		staleTime: 10_000,
	});
}

export function useMyTeams(enabled = true) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: myTeamsQueryKey(baseUrl),
		enabled: enabled && ready,
		queryFn: async (): Promise<CloudCpTeam[]> => (await client.listMyTeams()).teams,
		staleTime: 10_000,
	});
}

export function useTeam(orgId: string, teamId: string | undefined, external: boolean) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: teamQueryKey(baseUrl, orgId, teamId ?? "", external),
		enabled: ready && Boolean(teamId),
		queryFn: async (): Promise<CloudCpTeamDetail> => external ? client.getMyTeam(teamId!) : client.getTeam(orgId, teamId!),
	});
}

/**
 * Teams and known people matching `query` for a people field. An empty query
 * lists suggestions; results are cached per query so typing back is instant.
 */
export function useShareCandidates(orgId: string, query: string, enabled: boolean) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: shareCandidatesQueryKey(baseUrl, orgId, query),
		enabled: enabled && ready && Boolean(orgId),
		queryFn: async (): Promise<CloudCpShareCandidate[]> => (await client.listShareCandidates(orgId, query)).candidates,
		staleTime: 30_000,
		placeholderData: (previous) => previous,
	});
}

export function useInvalidateTeams() {
	const queryClient = useQueryClient();
	return useCallback(() => invalidateTeamsAndShares(queryClient), [queryClient]);
}

/** A plain email check, the same shape the control plane accepts. */
export function looksLikeEmail(value: string): boolean {
	return /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(value.trim());
}

/** Two letters for an avatar: initials of a name, else the start of an email. */
export function avatarInitials(name: string | undefined, email: string | undefined): string {
	const source = (name ?? "").trim();
	if (source) {
		const words = source.split(/\s+/).filter(Boolean);
		const letters = words.length > 1 ? words[0]![0]! + words[words.length - 1]![0]! : source.slice(0, 2);
		return letters.toUpperCase();
	}
	return (email ?? "?").slice(0, 2).toUpperCase();
}

/** Two letters for a team tile. */
export function teamInitials(name: string): string {
	const words = name.trim().split(/\s+/).filter(Boolean);
	if (words.length === 0) return "?";
	return (words.length > 1 ? words[0]![0]! + words[1]![0]! : words[0]!.slice(0, 2)).toUpperCase();
}
