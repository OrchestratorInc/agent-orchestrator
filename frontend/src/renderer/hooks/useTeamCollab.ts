import { useInfiniteQuery, useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useState, useSyncExternalStore } from "react";
import {
	probeTeamCollab,
	subscribeTeamCollabSupport,
	teamCollabSupport,
	teamCollabUnavailable,
	type TeamCollabSupport,
} from "../lib/cloud-team-collab";
import type { CloudCpFeedResponse, CloudCpFeedScope, CloudCpMentionCandidates } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";

/** How many feed rows a page asks for. */
export const FEED_PAGE_SIZE = 30;
/** How often an open feed refreshes, so "Working now" stays true. */
export const FEED_REFRESH_MS = 30_000;
/**
 * How often a session's pending suggestions are read while its chat is open:
 * only a safety net, since its suggestion events keep the list current.
 */
export const SUGGESTIONS_REFRESH_MS = 60_000;

export const teamCollabProbeKey = (baseUrl: string, userId: string) =>
	["cloud-team-collab-probe", baseUrl.replace(/\/+$/, ""), userId] as const;
export const teamFeedQueryRoot = (baseUrl: string, userId: string) =>
	["cloud-team-feed", baseUrl.replace(/\/+$/, ""), userId] as const;
export const teamFeedQueryKey = (baseUrl: string, userId: string, scope: CloudCpFeedScope) =>
	[...teamFeedQueryRoot(baseUrl, userId), scope] as const;
export const mentionCandidatesQueryKey = (baseUrl: string, orgId: string, sessionId: string, query: string) =>
	["cloud-mention-candidates", baseUrl.replace(/\/+$/, ""), orgId, sessionId, query] as const;
export const asideSuggestionsQueryKey = (orgId: string, sessionId: string) =>
	["cloud-aside-suggestions", orgId, sessionId] as const;

function retryUnlessUnavailable(failureCount: number, error: unknown): boolean {
	return !teamCollabUnavailable(error) && failureCount < 2;
}

const retryDelay = (attempt: number) => Math.min(500 * 2 ** attempt, 8_000);

/**
 * Whether the signed-in control plane serves asides, mentions and the team
 * feed. Asks once (a one-row page of the caller's own feed) and remembers the
 * answer for the app run; until it is known everything stays hidden.
 */
export function useTeamCollabSupport(): TeamCollabSupport {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const support = useSyncExternalStore(subscribeTeamCollabSupport, () => teamCollabSupport(baseUrl));
	useQuery({
		queryKey: teamCollabProbeKey(baseUrl, userId),
		enabled: ready && userId !== "" && support === "unknown",
		staleTime: Infinity,
		retry: retryUnlessUnavailable,
		retryDelay,
		queryFn: () => probeTeamCollab(baseUrl, () => client.getMyFeed({ scope: "mine", limit: 1 })).then(() => true),
	});
	return ready ? support : "unknown";
}

/**
 * One tab of the team feed, a page at a time, refreshed while it is open.
 * The first page carries "While you were away".
 */
export function useTeamFeed(scope: CloudCpFeedScope, enabled = true) {
	const { client, ready, baseUrl, userId } = useCloudCp();
	return useInfiniteQuery({
		queryKey: teamFeedQueryKey(baseUrl, userId, scope),
		enabled: enabled && ready && userId !== "",
		initialPageParam: undefined as string | undefined,
		retry: retryUnlessUnavailable,
		retryDelay,
		refetchInterval: FEED_REFRESH_MS,
		queryFn: ({ pageParam, signal }): Promise<CloudCpFeedResponse> =>
			probeTeamCollab(baseUrl, () => client.getMyFeed({ scope, limit: FEED_PAGE_SIZE, ...(pageParam ? { cursor: pageParam } : {}) }, { signal })),
		getNextPageParam: (last) => (last.page.hasMore && last.page.nextCursor ? last.page.nextCursor : undefined),
	});
}

/** Records a visit to the feed; the next "While you were away" counts from it. */
export function useMarkFeedSeen() {
	const { client, baseUrl, userId } = useCloudCp();
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: () => client.markMyFeedSeen(),
		onSuccess: () => {
			// Every tab's first page carries the card; they all start over.
			for (const query of queryClient.getQueryCache().findAll({ queryKey: teamFeedQueryRoot(baseUrl, userId) })) {
				queryClient.setQueryData<{ pages: CloudCpFeedResponse[]; pageParams: unknown[] }>(query.queryKey, (data) => data
					? { ...data, pages: data.pages.map((page) => ({ ...page, away: undefined })) }
					: data);
			}
		},
	});
}

/** What follows "@" in a composer, settled for a moment so typing asks once. */
export function useDebouncedValue<T>(value: T, delayMs: number): T {
	const [settled, setSettled] = useState(value);
	useEffect(() => {
		const timer = window.setTimeout(() => setSettled(value), delayMs);
		return () => window.clearTimeout(timer);
	}, [delayMs, value]);
	return settled;
}

/**
 * The people and teams a session's @ menu offers for a query. Asked only on a
 * control plane that has mentions, and only for a query that is not a path.
 */
export function useMentionCandidates(orgId: string | undefined, sessionId: string, query: string | undefined, enabled: boolean) {
	const { client, ready, baseUrl } = useCloudCp();
	const settled = useDebouncedValue(query, query ? 150 : 0);
	return useQuery<CloudCpMentionCandidates>({
		queryKey: mentionCandidatesQueryKey(baseUrl, orgId ?? "", sessionId, (settled ?? "").trim()),
		enabled: enabled && ready && Boolean(orgId) && settled !== undefined,
		staleTime: 30_000,
		retry: false,
		placeholderData: (previous) => previous,
		queryFn: ({ signal }) => probeTeamCollab(baseUrl, () =>
			client.listMentionCandidates(orgId ?? "", sessionId, { q: (settled ?? "").trim(), limit: 20 }, { signal })),
	});
}

/** A session's pending aside suggestions, oldest first. */
export function useAsideSuggestions(orgId: string | undefined, sessionId: string, enabled: boolean) {
	const { client, ready } = useCloudCp();
	return useQuery({
		queryKey: asideSuggestionsQueryKey(orgId ?? "", sessionId),
		enabled: enabled && ready && Boolean(orgId),
		refetchInterval: SUGGESTIONS_REFRESH_MS,
		retry: false,
		queryFn: async ({ signal }) => (await client.listSessionSuggestions(orgId ?? "", sessionId, { signal })).items,
	});
}
