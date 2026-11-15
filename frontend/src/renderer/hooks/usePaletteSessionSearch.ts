import { useQuery } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiErrorCode } from "../lib/api-client";
import { apiErrorException } from "../lib/api-error-exception";
import { clientForSessionHost } from "../lib/host-clients";
import { LOCAL_HOST, type HostId } from "../lib/hosts";

export type SessionSearchResponse = components["schemas"]["SessionSearchResponse"];
export type SessionSearchCoverage = components["schemas"]["SessionSearchCoverage"];

const SESSION_SEARCH_MIN_QUERY_CHARS = 2;

const SESSION_SEARCH_UNAVAILABLE = "SESSION_SEARCH_UNAVAILABLE";

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
		placeholderData: (previous, previousQuery) =>
			previousQuery?.queryKey.at(-1) === mode ? previous : undefined,
		staleTime: 5_000,
		retry: false,
		queryFn: async ({ signal }): Promise<SessionSearchResponse> => {
			const { data, error } = await clientForSessionHost(hostId).GET("/api/v1/search/sessions", {
				signal,
				params: { query: trimmed ? { q: trimmed, limit: 8 } : { sort: "recent", limit: 8 } },
			});
			if (error && !trimmed && apiErrorCode(error) === SESSION_SEARCH_UNAVAILABLE) {
				return noRecents;
			}
			if (error) throw apiErrorException(error, "Could not search past sessions");
			if (!data) throw new Error("Could not search past sessions");
			return data;
		},
	});
}

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
