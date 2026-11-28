import { hashKey, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useLayoutEffect, useMemo, useSyncExternalStore } from "react";
import { hasTrustedApiBaseUrl } from "../lib/api-client";
import { savedConversationQueryKey } from "../lib/local-chat-cache";
import type { WorkspaceSummary } from "../types/workspace";
import { conversationQueryKey, conversationQueryOptions } from "./useConversation";
import { workspaceQueryKey } from "./useWorkspaceQuery";

/** A local chat whose history can be read before its view renders (the sidebar's hover prefetch, on a switch). */
function readsChatHistory(queryClient: QueryClient, sessionId: string): boolean {
	if (!hasTrustedApiBaseUrl()) return false;
	const session = queryClient
		.getQueryData<WorkspaceSummary[]>(workspaceQueryKey)
		?.flatMap((workspace) => workspace.sessions)
		.find((candidate) => candidate.id === sessionId);
	// An imported chat has no conversation to read until it resumes on open.
	return session?.mode === "chat" && !session.cloud && !session.needsResume;
}

/**
 * Starts reading a local chat's history as a switch to it begins, alongside
 * the destination's render rather than after it: a keyboard or palette switch,
 * or a click faster than the sidebar's hover prefetch, otherwise asked only
 * once the destination had rendered its loading state. One page, and only for
 * a chat with nothing cached.
 *
 * Returns whether that chat's first read is still in flight, whoever started
 * it: the switch holds the session on screen until it lands (useHeldSession).
 */
export function useSwitchHistoryRead(sessionId: string | undefined): boolean {
	const queryClient = useQueryClient();
	useLayoutEffect(() => {
		if (!sessionId || queryClient.getQueryState(conversationQueryKey(sessionId))?.data !== undefined) return;
		if (!readsChatHistory(queryClient, sessionId)) return;
		void queryClient.prefetchInfiniteQuery(conversationQueryOptions(sessionId));
	}, [queryClient, sessionId]);
	const hash = useMemo(() => (sessionId ? hashKey(conversationQueryKey(sessionId)) : undefined), [sessionId]);
	const savedHash = useMemo(() => (sessionId ? hashKey(savedConversationQueryKey(sessionId)) : undefined), [sessionId]);
	const subscribe = useCallback((onChange: () => void) => queryClient.getQueryCache().subscribe(onChange), [queryClient]);
	// A boolean, so the chat's streamed updates (cache events too) re-render nothing.
	const firstReadInFlight = useCallback(() => {
		if (!hash || !savedHash) return false;
		const cache = queryClient.getQueryCache();
		const state = cache.get(hash)?.state;
		return state !== undefined && state.data === undefined && state.fetchStatus === "fetching" && cache.get(savedHash)?.state.data === undefined;
	}, [hash, queryClient, savedHash]);
	return useSyncExternalStore(subscribe, firstReadInFlight, firstReadInFlight);
}
