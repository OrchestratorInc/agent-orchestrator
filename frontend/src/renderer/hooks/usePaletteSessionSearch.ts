import { keepPreviousData, useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorMessage } from "../lib/api-client";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";
import {
	SESSION_SEARCH_MIN_QUERY_CHARS,
	sessionSearchQueryKey,
	type SessionSearchFilters,
} from "./useSessionSearch";

export type SessionSearchResponse = components["schemas"]["SessionSearchResponse"];
export type SessionSearchCoverage = components["schemas"]["SessionSearchCoverage"];

/**
 * One page of session-text search for the Cmd+K palette.
 *
 * It is the same endpoint the Search page uses, so an operator typed into the
 * palette behaves identically there and in `ao search`. The palette asks for a
 * short page: its list is a launcher, not a reading surface.
 *
 * With no text the daemon is asked for a filter-only browse, which returns
 * recent sessions. That is what makes the palette useful on an empty query: a
 * person opening it to "go back to that session" gets the recent list, the way
 * Raycast and Atuin show recents before a query is typed.
 */
/** Rows the palette asks each machine for: a launcher, not a results page. */
export const PALETTE_SESSION_SEARCH_LIMIT = 8;

export function usePaletteSessionSearch(
	query: string,
	hostId: HostId = LOCAL_HOST,
	enabled = true,
) {
	const trimmed = query.trim();
	const filters: SessionSearchFilters = {
		query: trimmed,
		projectId: "",
		harness: "",
		from: "",
		to: "",
	};
	const ready = trimmed.length === 0 || trimmed.length >= SESSION_SEARCH_MIN_QUERY_CHARS;
	return useQuery({
		queryKey: [...sessionSearchQueryKey(filters, hostId), "palette", trimmed === "" ? "recents" : "query"],
		enabled: enabled && ready,
		// Typing must refine the visible rows rather than blank them out.
		placeholderData: keepPreviousData,
		staleTime: 5_000,
		retry: false,
		queryFn: async (): Promise<SessionSearchResponse> => {
			// A filter-only browse needs no q; the daemon answers it with the
			// recent sessions for the scope. Passing an empty q keeps one endpoint.
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/search/sessions", {
				// The contract types q as required; an empty q plus sort=recent is the
				// recency browse the daemon accepts.
				params: { query: { q: trimmed, sort: trimmed ? undefined : "recent", limit: PALETTE_SESSION_SEARCH_LIMIT } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Could not search past sessions"));
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

/**
 * The in-session match list, used to step through matches with Enter and
 * Shift+Enter after a result opens.
 */
export function useSessionSearchMatches(
	sessionId: string | undefined,
	matchQuery: string,
	hostId: HostId = LOCAL_HOST,
	enabled = true,
) {
	const trimmed = matchQuery.trim();
	return useQuery({
		queryKey: ["session-search-matches", hostId, sessionId ?? "", trimmed],
		enabled: enabled && Boolean(sessionId) && trimmed.length > 0,
		staleTime: 30_000,
		retry: false,
		queryFn: async (): Promise<components["schemas"]["SessionSearchResult"][]> => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/sessions/{sessionId}/search/matches", {
				params: { path: { sessionId: sessionId! }, query: { q: trimmed } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Could not list matches"));
			return data?.matches ?? [];
		},
	});
}