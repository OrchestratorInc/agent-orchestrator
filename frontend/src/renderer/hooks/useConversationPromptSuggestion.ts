import { useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { clientForSessionHost } from "../lib/host-clients";
import type { ConversationMessage, ConversationSnapshot } from "../types/conversation";

const inactiveQueryKey = ["conversation-prompt-suggestion", "inactive"] as const;
const busyError = new Error("Prompt suggestion capacity is busy");
const staleError = new Error("Prompt suggestion controller state changed");
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
): PromptSuggestionState {
	return usePromptSuggestion(snapshot, enabled, hostId ?? "local", async ({ sessionId, turnId, signal }) => {
		const { data, error, response } = await clientForSessionHost(hostId).POST(
			"/api/v1/sessions/{sessionId}/conversation/prompt-suggestion",
			{ params: { path: { sessionId } }, body: { turnId }, signal },
		);
		if (error?.code === "CHAT_PROMPT_SUGGESTION_BUSY") throw promptSuggestionBusy;
		if (response?.status === 409) throw promptSuggestionStale;
		return error || data?.turnId !== turnId ? "" : data.prompt;
	});
}

/** Thrown by a suggestion fetch when the suggester is busy, so it is asked again shortly. */
export const promptSuggestionBusy = busyError;
/** Thrown by a suggestion fetch when the controller state changed under it, so the answer is not cached. */
export const promptSuggestionStale = staleError;

/** Why a draft polish produced no text, in the terms the composer shows. */
export type PromptOptimizationFailure = "busy" | "stale" | "unavailable" | "too-long";

export interface PromptOptimizationRequestInput {
	sessionId: string;
	turnId: string;
	draft: string;
	signal: AbortSignal;
}

/**
 * The daemon's polish of the draft the human is writing.
 *
 * This is a manual action, not a background hint: it is never started by a
 * render, so it keeps no query entry and no cache key. A draft that was polished
 * once is not silently reused after the user edits it — asking again is the
 * intent, and the answer is shown as a proposal that only an explicit accept
 * replaces the draft with.
 */
export function usePromptOptimization(options: {
	enabled: boolean;
	request: (input: PromptOptimizationRequestInput) => Promise<string>;
}) {
	const { enabled, request } = options;
	const [state, setState] = useState<{ pending: boolean; failure?: PromptOptimizationFailure }>({ pending: false });
	const inFlight = useRef<AbortController | null>(null);
	const mounted = useRef(true);
	useEffect(() => () => {
		mounted.current = false;
		inFlight.current?.abort();
	}, []);

	const optimize = useCallback(async (sessionId: string, turnId: string, draft: string): Promise<string | undefined> => {
		// One polish at a time: a second click while the first is running would spend
		// a second provider call for an answer the first already supersedes.
		if (!enabled || inFlight.current) return undefined;
		const controller = new AbortController();
		inFlight.current = controller;
		setState({ pending: true });
		try {
			const text = (await request({ sessionId, turnId, draft, signal: controller.signal })).trim();
			if (mounted.current && inFlight.current === controller) setState({ pending: false });
			return text || undefined;
		} catch (error) {
			if (!mounted.current || inFlight.current !== controller) return undefined;
			// The composer shows why nothing came back, and keeps the draft either way.
			const failure: PromptOptimizationFailure = error === promptSuggestionBusy ? "busy"
				: error === promptSuggestionStale ? "stale"
					: error === promptSuggestionTooLong ? "too-long"
						: "unavailable";
			setState({ pending: false, failure });
			return undefined;
		} finally {
			if (inFlight.current === controller) inFlight.current = null;
		}
	}, [enabled, request]);

	return { ...state, optimize };
}

/** The daemon-backed polish request for a local or remote session. */
export function localPromptOptimizationRequest(hostId?: string) {
	return async ({ sessionId, turnId, draft, signal }: PromptOptimizationRequestInput): Promise<string> => {
		const { data, error, response } = await clientForSessionHost(hostId).POST(
			"/api/v1/sessions/{sessionId}/conversation/prompt-suggestion",
			{ params: { path: { sessionId } }, body: { turnId, draft }, signal },
		);
		if (error?.code === "CHAT_PROMPT_SUGGESTION_BUSY") throw promptSuggestionBusy;
		if (error?.code === "CHAT_PROMPT_DRAFT_TOO_LONG") throw promptSuggestionTooLong;
		if (response?.status === 409) throw promptSuggestionStale;
		if (error) throw error;
		return data?.turnId === turnId ? data.prompt : "";
	};
}

/** Thrown when the daemon refuses a draft longer than it will polish. */
export const promptSuggestionTooLong = new Error("Prompt draft is too long");

/**
 * The turn both suggestion features are keyed to: the latest completed turn,
 * with nothing running, queued, streaming, or rolled back, once the controller
 * is ready. A polish reads the same settled conversation, so it shares this.
 */
export function settledSuggestionTurnId(snapshot: ConversationSnapshot | undefined): string | undefined {
	const latestTurn = snapshot?.turns.filter((turn) => !turn.rolledBack).at(-1);
	return snapshot?.mode === "chat" && snapshot.controller.state === "ready" &&
		latestTurn?.state === "completed" &&
		!snapshot.turns.some((turn) => turn.state === "running" || turn.state === "queued") &&
		!snapshot.items.some((item) => item.kind === "message" && item.streaming)
		? latestTurn.id : undefined;
}

export interface PromptSuggestionState {
	suggestion: { id: string; text: string; onDismiss: () => void } | undefined;
	/** The suggestion fetch is in flight, including a user-requested retry. */
	pending: boolean;
	/** Ask again for this settled turn, e.g. after the user pressed the button. */
	refresh: () => void;
}

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
): PromptSuggestionState {
	const queryClient = useQueryClient();
	const dismissedContexts = dismissedContextsByClient.get(queryClient) ?? new Set<string>();
	dismissedContextsByClient.set(queryClient, dismissedContexts);
	const turnId = settledSuggestionTurnId(snapshot);
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
			if (!turnId || !current || signal.aborted) return null;
			try {
				const prompt = (await fetchPrompt({ sessionId, turnId, snapshot: current, signal })).trim();
				if (!prompt) return null;
				return { id, text: prompt };
			} catch (error) {
				if (signal.aborted || error === busyError || error === staleError) throw error;
				// Successful omissions are cached like hints; capacity errors stay transient.
				return null;
			}
		},
	});
	// The composer's own control asks again on purpose: that is the difference
	// between a hint nobody wanted and a prediction the user requested.
	const refresh = useCallback(() => {
		dismissedContexts.delete(id);
		queryClient.setQueryData(queryKey, null);
		void queryClient.invalidateQueries({ queryKey, exact: true, refetchType: "active" });
	}, [dismissedContexts, id, queryClient, queryKey]);
	const suggestion = useMemo(() => enabled && turnId && !dismissed && query.data
		? { ...query.data, onDismiss } : undefined,
	[enabled, turnId, dismissed, query.data, onDismiss]);
	return useMemo(() => ({ suggestion, pending: query.isFetching, refresh }),
		[suggestion, query.isFetching, refresh]);
}
