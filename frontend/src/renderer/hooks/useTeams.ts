import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback } from "react";
import { CloudCpError, type CloudCpShareCandidate, type CloudCpTeam, type CloudCpTeamDetail } from "../lib/cloud-cp";
import { useCloudCp } from "./useCloudCp";

export function retryCloudRead(failureCount: number, error: unknown): boolean {
	if (error instanceof CloudCpError && error.status >= 400 && error.status < 500) return false;
	return failureCount < 1;
}

export function teamsUnavailable(error: unknown): boolean {
	return error instanceof CloudCpError && error.status === 404 && !error.code;
}

export function teamsQueryKey(baseUrl: string, orgId: string) {
	return ["cloud-teams", baseUrl, orgId] as const;
}

export function myTeamsQueryKey(baseUrl: string) {
	return ["cloud-my-teams", baseUrl] as const;
}

export function teamQueryKey(baseUrl: string, orgId: string, teamId: string, external: boolean) {
	return ["cloud-team", baseUrl, orgId, teamId, external] as const;
}

export function shareAccessQueryKey(baseUrl: string, orgId: string, projectId: string, sessionId: string | undefined) {
	return ["cloud-share-access", baseUrl, orgId, projectId, sessionId ?? ""] as const;
}

export function teamShareAccessQueryKey(baseUrl: string, orgId: string, projectId: string, sessionId: string | undefined, teamId: string) {
	return ["cloud-team-share-access", baseUrl, orgId, projectId, sessionId ?? "", teamId] as const;
}

export function shareCandidatesQueryKey(baseUrl: string, orgId: string, query: string) {
	return ["cloud-share-candidates", baseUrl, orgId, query] as const;
}

export function teamImpactQueryKey(baseUrl: string, orgId: string, teamId: string, kind: string, memberId: string) {
	return ["cloud-team-impact", baseUrl, orgId, teamId, kind, memberId] as const;
}

export function workspaceMembersQueryKey(baseUrl: string, orgId: string) {
	return ["cloud-workspace-members", baseUrl, orgId] as const;
}

export function invalidateTeamsAndShares(queryClient: QueryClient) {
	for (const key of ["cloud-teams", "cloud-my-teams", "cloud-team", "cloud-share-access", "cloud-team-share-access", "cloud-share-candidates", "cloud-session-participants", "cloud-shared-workspaces"]) {
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
		retry: retryCloudRead,
	});
}

export function useMyTeams(enabled = true) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: myTeamsQueryKey(baseUrl),
		enabled: enabled && ready,
		queryFn: async (): Promise<CloudCpTeam[]> => (await client.listMyTeams()).teams,
		staleTime: 10_000,
		retry: retryCloudRead,
	});
}

export function useTeam(orgId: string, teamId: string | undefined, external: boolean) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: teamQueryKey(baseUrl, orgId, teamId ?? "", external),
		enabled: ready && Boolean(teamId),
		queryFn: async (): Promise<CloudCpTeamDetail> => external ? client.getMyTeam(teamId!) : client.getTeam(orgId, teamId!),
		retry: retryCloudRead,
	});
}

export function useShareCandidates(orgId: string, query: string, enabled: boolean) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: shareCandidatesQueryKey(baseUrl, orgId, query),
		enabled: enabled && ready && Boolean(orgId),
		queryFn: async (): Promise<CloudCpShareCandidate[]> => (await client.listShareCandidates(orgId, query)).candidates,
		staleTime: 30_000,
		placeholderData: (previous) => previous,
		retry: retryCloudRead,
	});
}

export function useWorkspaceMemberCount(orgId: string, enabled: boolean) {
	const { client, baseUrl, ready } = useCloudCp();
	return useQuery({
		queryKey: workspaceMembersQueryKey(baseUrl, orgId),
		enabled: enabled && ready && Boolean(orgId),
		queryFn: async ({ signal }): Promise<number> => (await client.listOrgMembers(orgId, { signal })).members.length,
		staleTime: 30_000,
		retry: retryCloudRead,
	});
}

export function useInvalidateTeams() {
	const queryClient = useQueryClient();
	return useCallback(() => invalidateTeamsAndShares(queryClient), [queryClient]);
}

export function looksLikeEmail(value: string): boolean {
	return /^[^\s@]+@[^\s@]+\.[^\s@]{2,}$/.test(value.trim());
}

export function avatarInitials(name: string | undefined, email: string | undefined): string {
	const source = (name ?? "").trim();
	if (source) {
		const words = source.split(/\s+/).filter(Boolean);
		const letters = words.length > 1 ? words[0]![0]! + words[words.length - 1]![0]! : source.slice(0, 2);
		return letters.toUpperCase();
	}
	return (email ?? "?").slice(0, 2).toUpperCase();
}

export function teamInitials(name: string): string {
	const words = name.trim().split(/\s+/).filter(Boolean);
	if (words.length === 0) return "?";
	return (words.length > 1 ? words[0]![0]! + words[1]![0]! : words[0]!.slice(0, 2)).toUpperCase();
}
