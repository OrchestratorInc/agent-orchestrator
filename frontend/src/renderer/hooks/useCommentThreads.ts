import { useInfiniteQuery, useQuery } from "@tanstack/react-query";
import { cloudCommentThreadsKey, cloudUnreadThreadsKey } from "../lib/cloud-asides";
import { CloudCpError } from "../lib/cloud-cp/errors";
import type { CloudCpCommentThread, CloudCpCommentThreadPage } from "../lib/cloud-cp/types";
import { useCloudCp } from "./useCloudCp";

// The threads view: the comment threads this person is in, across every
// organization and session they can open (GET /me/comment-threads), and the
// sidebar's unread count. A control plane without the view answers 404, and
// then the Threads item stays hidden and notifications open the session.

/** How many threads one page of the view reads (the control plane's maximum). */
export const COMMENT_THREADS_PAGE = 50;

/** How often the Threads badge is read again while nothing else refreshed it. */
const UNREAD_COUNT_REFRESH_MS = 60_000;

/** The Threads badge, and whether this control plane has the threads view at all. */
export function useUnreadCommentThreads(): { count: number; supported: boolean } {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const query = useQuery<{ count: number }>({
		queryKey: cloudUnreadThreadsKey(baseUrl, userId),
		enabled: ready && userId !== "",
		staleTime: 30_000,
		refetchInterval: UNREAD_COUNT_REFRESH_MS,
		refetchOnWindowFocus: true,
		// A control plane without the view says so once; asking again changes nothing.
		retry: (failures, error) => !(error instanceof CloudCpError && error.status === 404) && failures < 1,
		queryFn: ({ signal }) => client.countMyUnreadCommentThreads({ signal }),
	});
	// Once it answered, a passing failure (offline, a 500) keeps the item; only a 404 says there is no view.
	const missing = query.error instanceof CloudCpError && query.error.status === 404;
	return { count: query.data?.count ?? 0, supported: query.data !== undefined && !missing };
}

/** One tab of the threads view, read 50 at a time. Rows are keyed by asideId: one that moved between pages shows once. */
export function useCommentThreadList(status: "open" | "resolved", unread: boolean) {
	const { client, ready, baseUrl, userId } = useCloudCp();
	const query = useInfiniteQuery({
		queryKey: cloudCommentThreadsKey(baseUrl, userId, status, unread),
		enabled: ready && userId !== "",
		initialPageParam: undefined as string | undefined,
		staleTime: 15_000,
		refetchOnWindowFocus: true,
		retry: (failures, error) => !(error instanceof CloudCpError && error.status === 404) && failures < 1,
		queryFn: ({ pageParam, signal }) => client.listMyCommentThreads({ status, ...(unread ? { unread: true } : {}), ...(pageParam ? { cursor: pageParam } : {}), limit: COMMENT_THREADS_PAGE }, { signal }),
		getNextPageParam: (page: CloudCpCommentThreadPage) => (page.hasMore && page.nextCursor ? page.nextCursor : undefined),
	});
	return { ...query, rows: uniqueThreadRows(query.data?.pages) };
}

/** Every page's rows once each, in the order the control plane gave them. */
export function uniqueThreadRows(pages: readonly CloudCpCommentThreadPage[] | undefined): CloudCpCommentThread[] {
	const seen = new Set<string>();
	const rows: CloudCpCommentThread[] = [];
	for (const page of pages ?? []) {
		for (const row of page.items) {
			if (seen.has(row.aside.asideId)) continue;
			seen.add(row.aside.asideId);
			rows.push(row);
		}
	}
	return rows;
}
