import { keepPreviousData, useQueries, useQuery, type UseQueryResult } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";

export type SessionSearchResult = components["schemas"]["SessionSearchResult"];
export type SessionSearchCoverage = components["schemas"]["SessionSearchCoverage"];
export type SessionSearchResponse = components["schemas"]["SessionSearchResponse"];

/** Filters a search view sends. Empty strings mean "no filter". */
export type SessionSearchFilters = {
	query: string;
	projectId: string;
	harness: string;
	/** Inclusive lower bound as YYYY-MM-DD, or "" for none. */
	from: string;
	/** Inclusive upper bound as YYYY-MM-DD, or "" for none. */
	to: string;
	/** Order: relevance while searching, recent while browsing. */
	sort?: "relevance" | "recent";
	/** Page size; defaults to SESSION_SEARCH_PAGE_SIZE. */
	limit?: number;
	/** Rows to skip, for paging. */
	offset?: number;
};

export const SESSION_SEARCH_PAGE_SIZE = 25;
/** Below this the index would match nearly everything, which is noise. */
export const SESSION_SEARCH_MIN_QUERY_CHARS = 2;

export function sessionSearchQueryKey(filters: SessionSearchFilters, hostId: HostId) {
	return [
		"session-search",
		hostId,
		filters.query.trim(),
		filters.projectId,
		filters.harness,
		filters.from,
		filters.to,
		filters.sort ?? "relevance",
		filters.limit ?? SESSION_SEARCH_PAGE_SIZE,
		filters.offset ?? 0,
	] as const;
}

function sessionSearchQueryOptions(filters: SessionSearchFilters, hostId: HostId) {
	const query = filters.query.trim();
	const filtersActive = Boolean(
		filters.projectId || filters.harness || filters.from || filters.to || filters.sort === "recent",
	);
	return {
		queryKey: sessionSearchQueryKey(filters, hostId),
		// A filter-only browse is a search too: opening the page with a project
		// selected should show that project's sessions rather than nothing.
		enabled: query.length >= SESSION_SEARCH_MIN_QUERY_CHARS || (query.length === 0 && filtersActive),
		// Typing should refine the current list rather than blank it out.
		placeholderData: keepPreviousData,
		staleTime: 10_000,
		retry: 1,
		queryFn: async (): Promise<SessionSearchResponse> => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/search/sessions", {
				params: {
					query: {
						// The generated contract types q as required, but the daemon
						// accepts a filter-only or recency browse without it. An empty
						// string is how that is expressed on the wire.
						q: query,
						...(filters.projectId ? { projectId: filters.projectId } : {}),
						...(filters.harness ? { harness: filters.harness } : {}),
						...(filters.from ? { from: filters.from } : {}),
						...(filters.to ? { to: filters.to } : {}),
						...(filters.sort ? { sort: filters.sort } : {}),
						limit: filters.limit ?? SESSION_SEARCH_PAGE_SIZE,
						...(filters.offset ? { offset: filters.offset } : {}),
					},
				},
			});
			if (error) throw apiErrorException(error, "Could not search past sessions");
			if (!data) throw new Error("Could not search past sessions");
			return data;
		},
	};
}

/**
 * One page of ranked results over past sessions.
 *
 * The daemon owns ranking, filtering, and snippet offsets; this hook only
 * transports the query. A short query is not sent at all — matching one
 * character across every past session returns the whole archive.
 */
export function useSessionSearch(filters: SessionSearchFilters, hostId: HostId = LOCAL_HOST) {
	return useQuery(sessionSearchQueryOptions(filters, hostId));
}

/** The first pages of a search, joined into one list. */
export type SessionSearchPages = {
	/** The first page's response with every fetched page's results, in order. */
	data: SessionSearchResponse | undefined;
	isLoading: boolean;
	isFetching: boolean;
	/** The first page failed: there is nothing to show. */
	error: Error | null;
	/**
	 * A later page failed after its retry. The pages before it stay on screen and
	 * paging stops there until retryPage, so a later page cannot be shown past a
	 * gap.
	 */
	pageError: Error | null;
	/** Asks every failed page again. */
	retryPage: () => void;
	/** The last page came back short: there is nothing more to fetch. */
	exhausted: boolean;
};

function joinSessionSearchPages(pages: UseQueryResult<SessionSearchResponse>[]): SessionSearchPages {
	const first = pages[0];
	const last = pages.at(-1);
	// Rows stop at the first page that failed: what follows it would leave a gap.
	const failedAt = pages.findIndex((page, index) => index > 0 && page.error);
	const shown = failedAt > 0 ? pages.slice(0, failedAt) : pages;
	const seen = new Set<string>();
	const results: SessionSearchResult[] = [];
	for (const page of shown) {
		for (const result of page.data?.results ?? []) {
			// A session can move to a later page when new work is indexed between
			// requests; it is shown once, at its first place.
			if (seen.has(result.sessionId)) continue;
			seen.add(result.sessionId);
			results.push(result);
		}
	}
	const lastResults = last?.data?.results;
	return {
		data: first?.data ? { ...first.data, results } : undefined,
		isLoading: first?.isLoading ?? false,
		isFetching: pages.some((page) => page.isFetching),
		error: first?.error ?? null,
		pageError: failedAt > 0 ? pages[failedAt]!.error : null,
		retryPage: () => {
			for (const page of pages) if (page.error) void page.refetch();
		},
		exhausted: failedAt < 0 && Boolean(last?.data && !last.isPlaceholderData && (lastResults?.length ?? 0) < SESSION_SEARCH_PAGE_SIZE),
	};
}

/**
 * The first `pages` pages of a search, each its own request of
 * SESSION_SEARCH_PAGE_SIZE rows at the next offset.
 *
 * The daemon caps one request at 100 rows (SessionSearchMaxLimit), so growing a
 * single request's limit stopped at the first 100 sessions: every "Show more"
 * past them returned the same rows. Fixed-size pages keep every request within
 * the cap, and each page is cached on its own, so "Show more" fetches only the
 * new one.
 */
export function useSessionSearchPages(
	filters: SessionSearchFilters,
	pages: number,
	hostId: HostId = LOCAL_HOST,
): SessionSearchPages {
	return useQueries({
		queries: Array.from({ length: Math.max(1, pages) }, (_, page) =>
			sessionSearchQueryOptions({ ...filters, limit: SESSION_SEARCH_PAGE_SIZE, offset: page * SESSION_SEARCH_PAGE_SIZE }, hostId),
		),
		combine: joinSessionSearchPages,
	});
}

/**
 * Splits a result's snippet into plain text and highlighted spans.
 *
 * The daemon already returns offsets, so this only slices — it never
 * interprets markup, which keeps arbitrary transcript text inert.
 */
export function snippetSegments(
	snippet: string,
	highlights: ReadonlyArray<{ start: number; end: number }> | undefined,
): Array<{ text: string; match: boolean }> {
	if (!highlights?.length) return snippet ? [{ text: snippet, match: false }] : [];
	const segments: Array<{ text: string; match: boolean }> = [];
	let cursor = 0;
	for (const span of [...highlights].sort((left, right) => left.start - right.start)) {
		// Clamp before validating: an out-of-range span must not produce an empty
		// match at the end of the snippet, and an overlapping one must not repeat
		// text the previous span already covered.
		const start = Math.max(Math.min(span.start, snippet.length), cursor);
		const end = Math.min(span.end, snippet.length);
		if (end <= start) continue;
		if (start > cursor) segments.push({ text: snippet.slice(cursor, start), match: false });
		segments.push({ text: snippet.slice(start, end), match: true });
		cursor = end;
	}
	if (cursor < snippet.length) segments.push({ text: snippet.slice(cursor), match: false });
	return segments;
}