import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useMemo, useRef } from "react";
import { clientForSessionHost } from "../lib/host-clients";
import type { ConversationMessage, ConversationSnapshot } from "../types/conversation";

const inactiveQueryKey = ["conversation-prompt-suggestion", "inactive"] as const;
const busyError = new Error("Prompt suggestion capacity is busy");
// Navigation and query GC must not undo Escape. Bound this to recent contexts and
// let the QueryClient's lifetime own it, rather than retaining every past turn.
const dismissedContextsByClient = new WeakMap<QueryClient, Set<string>>();

function replyFingerprint(text: string): string {
	let first = 2166136261;
	let second = 5381;
	for (let i = 0; i < text.length; i++) {
		const code = text.charCodeAt(i);
		first = Math.imul(first ^ code, 16777619);
		second = Math.imul(second ^ code, 1566083941);
	}
	return `${text.length}:${first >>> 0}:${second >>> 0}`;
}

export function useConversationPromptSuggestion(
	snapshot: ConversationSnapshot | undefined,
	enabled: boolean,
	hostId?: string,
): { id: string; text: string; onDismiss: () => void } | undefined {
	return usePromptSuggestion(snapshot, enabled, hostId ?? "local", async ({ sessionId, turnId, signal }) => {
		const { data, error } = await clientForSessionHost(hostId).POST(
			"/api/v1/sessions/{sessionId}/conversation/prompt-suggestion",
			{ params: { path: { sessionId } }, body: { turnId }, signal },
		);
		if (error?.code === "CHAT_PROMPT_SUGGESTION_BUSY") throw promptSuggestionBusy;
		return error || data?.turnId !== turnId ? "" : data.prompt;
	});
}

/** Thrown by a suggestion fetch when the suggester is busy, so it is asked again shortly. */
export const promptSuggestionBusy = busyError;

/**
 * The next-message suggestion after a settled turn, fetched by `fetchPrompt`
 * for the turn and its reply. `scope` keys where the conversation lives (a
 * local or remote daemon, or Cloud), so dismissals and answers never cross.
 */
export function usePromptSuggestion(
	snapshot: ConversationSnapshot | undefined,
	enabled: boolean,
	scope: string,
	fetchPrompt: (input: { sessionId: string; turnId: string; snapshot: ConversationSnapshot; signal: AbortSignal }) => Promise<string>,
): { id: string; text: string; onDismiss: () => void } | undefined {
	const queryClient = useQueryClient();
	const dismissedContexts = dismissedContextsByClient.get(queryClient) ?? new Set<string>();
	dismissedContextsByClient.set(queryClient, dismissedContexts);
	const latestTurn = snapshot?.turns.filter((turn) => !turn.rolledBack).at(-1);
	const turnId = snapshot?.mode === "chat" && snapshot.controller.state === "ready" &&
		latestTurn?.state === "completed" &&
		!snapshot.turns.some((turn) => turn.state === "running" || turn.state === "queued") &&
		!snapshot.items.some((item) => item.kind === "message" && item.streaming)
		? latestTurn.id : undefined;
	let reply: ConversationMessage | undefined;
	if (turnId && snapshot) {
		for (let i = snapshot.items.length - 1; i >= 0; i--) {
			const item = snapshot.items[i];
			if (item.kind === "message" && item.role === "assistant") { reply = item; break; }
		}
	}
	// This is only a content version of the settled answer. The daemon owns context
	// selection/truncation; streaming text and activity-only replay are never hashed.
	const sourceVersion = useMemo(() => replyFingerprint(reply?.text ?? ""), [reply?.text]);
	const sessionId = snapshot?.sessionId ?? "";
	const queryKey = useMemo(() => turnId
		? ["conversation-prompt-suggestion", scope, sessionId, snapshot?.conversationId ?? "", snapshot?.activeBranchId ?? "", turnId, reply?.id ?? "", sourceVersion]
		: inactiveQueryKey,
	[scope, sessionId, snapshot?.conversationId, snapshot?.activeBranchId, turnId, reply?.id, sourceVersion]);
	// The query reads the snapshot it was keyed for; later renders only change the key.
	const snapshotRef = useRef(snapshot);
	snapshotRef.current = snapshot;
	const id = useMemo(() => JSON.stringify(queryKey), [queryKey]);
	const dismissed = dismissedContexts.has(id);
	const onDismiss = useCallback(() => {
		dismissedContexts.delete(id);
		dismissedContexts.add(id);
		if (dismissedContexts.size > 256) dismissedContexts.delete(dismissedContexts.values().next().value!);
		queryClient.setQueryData(queryKey, null);
	}, [dismissedContexts, id, queryClient, queryKey]);
	const query = useQuery({
		queryKey,
		enabled: enabled && Boolean(turnId) && !dismissed,
		staleTime: Infinity,
		gcTime: 5 * 60_000,
		retry: (attempt, error) => error === busyError && attempt < 2,
		retryDelay: 1000,
		queryFn: async ({ signal }) => {
			const current = snapshotRef.current;
			if (!turnId || !current) return null;
			try {
				const prompt = (await fetchPrompt({ sessionId, turnId, snapshot: current, signal })).trim();
				if (!prompt) return null;
				return { id, text: prompt };
			} catch (error) {
				if (signal.aborted || error === busyError) throw error;
				// Successful omissions are cached like hints; capacity errors stay transient.
				return null;
			}
		},
	});
	return useMemo(() => enabled && turnId && !dismissed && query.data
		? { ...query.data, onDismiss } : undefined,
	[enabled, turnId, dismissed, query.data, onDismiss]);
}
