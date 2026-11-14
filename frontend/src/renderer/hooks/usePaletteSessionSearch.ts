import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorCode } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";

export type SessionSearchResponse = components["schemas"]["SessionSearchResponse"];
export type SessionSearchCoverage = components["schemas"]["SessionSearchCoverage"];

/** Below this the index would match nearly everything, which is noise. */
const SESSION_SEARCH_MIN_QUERY_CHARS = 2;

const SESSION_SEARCH_UNAVAILABLE = "SESSION_SEARCH_UNAVAILABLE";

/** The empty recents list a daemon with search turned off stands for. */
const noRecents: SessionSearchResponse = {
	coverage: {
		state: "unavailable",
		indexedDocs: 0,
		indexedSources: 0,
		pendingSources: 0,
		unavailableSources: 0,
		unsupportedSources: 0,
	},
	group: "session",
	limit: 8,
	offset: 0,
	query: "",
	results: [],
	sort: "recent",
	total: 0,
};

/**
 * One page of session-text search for the Cmd+K palette.
 *
 * It is the same endpoint `ao search` uses, so an operator typed into the
 * palette behaves identically there. The palette asks for a short page: its
 * list is a launcher, not a reading surface.
 *
 * With no text the daemon is asked for a filter-only browse, which returns
 * recent sessions. That is what makes the palette useful on an empty query: a
 * person opening it to "go back to that session" gets the recent list, the way
 * Raycast and Atuin show recents before a query is typed.
 */
export function usePaletteSessionSearch(
	query: string,
	hostId: HostId = LOCAL_HOST,
	enabled = true,
) {
	const trimmed = query.trim();
	const ready = trimmed.length === 0 || trimmed.length >= SESSION_SEARCH_MIN_QUERY_CHARS;
	const mode = trimmed === "" ? "recents" : "query";
	return useQuery<SessionSearchResponse>({
		queryKey: ["session-search", hostId, trimmed, "palette", mode],
		enabled: enabled && ready,
		// Typing refines the previous query's rows rather than blanking them. Recents
		// are not kept for a query: they would show as matches until it answers.
		placeholderData: (previous, previousQuery) =>
			previousQuery?.queryKey.at(-1) === mode ? previous : undefined,
		staleTime: 5_000,
		retry: false,
		// The palette debounces the query; the signal stops a search the next
		// query replaced before it answered.
		queryFn: async ({ signal }): Promise<SessionSearchResponse> => {
			// With no words, sort=recent asks the same endpoint for recent sessions.
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/search/sessions", {
				signal,
				params: { query: trimmed ? { q: trimmed, limit: 8 } : { sort: "recent", limit: 8 } },
			});
			// Search is optional: a daemon whose index could not be opened answers
			// SESSION_SEARCH_UNAVAILABLE. Opening the palette then shows no recents
			// rather than a warning; a typed search still says why it found nothing.
			if (error && !trimmed && apiErrorCode(error) === SESSION_SEARCH_UNAVAILABLE) {
				return noRecents;
			}
			if (error) throw apiErrorException(error, "Could not search past sessions");
			if (!data) throw new Error("Could not search past sessions");
			return data;
		},
	});
}

/**
 * Whether the index can answer at all, for the palette's status line. A missing
 * or unavailable index must not look like "no results".
 *
 * An unavailable status resolves to null rather than rejecting: the status line
 * is a note, and a failing note must never surface as an error beside a search
 * that worked. (React Query v5 treats a resolved `undefined` as an error, so
 * null is the "no status" value.)
 */
export function useSessionSearchIndexStatus(hostId: HostId = LOCAL_HOST, enabled = true) {
	return useQuery({
		queryKey: ["session-search-index-status", hostId],
		enabled,
		staleTime: 30_000,
		retry: false,
		queryFn: async (): Promise<SessionSearchCoverage | null> => {
			try {
				const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/search/index-status");
				if (error) return null;
				return data?.coverage ?? null;
			} catch {
				return null;
			}
		},
	});
}
