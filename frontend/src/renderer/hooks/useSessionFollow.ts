import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { cloudInboxUnavailable } from "../lib/cloud-notifications";
import type { CloudCpFollowLevel, CloudCpSessionFollower } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";
import { useCloudInboxSupport } from "./useCloudNotifications";

export const sessionFollowQueryKey = (baseUrl: string, userId: string, orgId: string, sessionId: string) =>
	["cloud-session-follow", baseUrl.replace(/\/+$/, ""), userId, orgId, sessionId] as const;

export const sessionFollowersQueryKey = (baseUrl: string, userId: string, orgId: string, sessionId: string) =>
	["cloud-session-followers", baseUrl.replace(/\/+$/, ""), userId, orgId, sessionId] as const;

function retryFollowRead(failureCount: number, error: unknown): boolean {
	return !cloudInboxUnavailable(error) && failureCount < 2;
}

/**
 * How the signed-in person follows a cloud session, who else is notified
 * about it, and a way to change their own level. Only on a control plane with
 * team notifications: `available` stays false until the inbox has said so,
 * and on one that predates them.
 */
export function useSessionFollow(orgId: string | undefined, sessionId: string | undefined, { followers = false }: { followers?: boolean } = {}) {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const queryClient = useQueryClient();
	const support = useCloudInboxSupport(baseUrl);
	const enabled = ready && userId !== "" && Boolean(orgId && sessionId) && support === "supported";
	const followKey = sessionFollowQueryKey(baseUrl, userId, orgId ?? "", sessionId ?? "");
	const followersKey = sessionFollowersQueryKey(baseUrl, userId, orgId ?? "", sessionId ?? "");
	const follow = useQuery({
		queryKey: followKey,
		enabled,
		retry: retryFollowRead,
		queryFn: () => client.getSessionFollow(orgId ?? "", sessionId ?? ""),
	});
	const followerList = useQuery({
		queryKey: followersKey,
		enabled: enabled && followers,
		retry: retryFollowRead,
		queryFn: async () => (await client.listSessionFollowers(orgId ?? "", sessionId ?? "")).items,
	});
	const setLevel = useMutation({
		mutationFn: (level: CloudCpFollowLevel) => client.setSessionFollow(orgId ?? "", sessionId ?? "", level),
		onSuccess: (next: CloudCpSessionFollower) => {
			queryClient.setQueryData(followKey, next);
			void queryClient.invalidateQueries({ queryKey: followersKey });
		},
	});
	return {
		available: enabled && follow.isSuccess,
		follow: follow.data,
		followers: followerList.data,
		followersLoading: followerList.isLoading,
		followersError: followerList.isError,
		setLevel: setLevel.mutateAsync,
		saving: setLevel.isPending,
		saveError: setLevel.isError,
		me: userId,
	};
}
