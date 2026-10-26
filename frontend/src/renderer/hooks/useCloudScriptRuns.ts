import { hashKey, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { CloudCpError, type CloudCpClientEvent } from "../lib/cloud-cp";
import { subscribeSessionEventsBridged } from "../lib/cloud-cp/stream-bridge";
import { cloudSessionScriptsEnded, groupScriptRuns, mergeScriptStepEvents, SCRIPT_RUN_END_EVENTS, SCRIPT_STEP_EVENT, type ScriptRun } from "../lib/cloud-script-runs";
import { useCloudCp } from "./useCloudCp";

/**
 * A session's `workspace.script.step` events, kept apart from the chat event
 * list so terminal sessions (which never load that list) can show their Setup
 * and Cleanup runs too.
 */
export function cloudScriptStepsQueryKey(orgId: string, sessionId: string) {
	return ["cloud-script-steps", orgId, sessionId] as const;
}

/** The chat surface's own event list (CloudSessionChatSurface); read, never fetched, here. */
function cloudChatEventsQueryKey(orgId: string, sessionId: string) {
	return ["cloud-chat-events", orgId, sessionId] as const;
}

/**
 * Folds live events into the script-step cache. The startup loader calls this
 * from the stream and poll it already runs, so the inspector and the failure
 * card update without a request of their own.
 */
export function recordCloudScriptStepEvents(queryClient: QueryClient, orgId: string, sessionId: string, events: readonly CloudCpClientEvent[]) {
	if (!orgId || !events.some((event) => event.type === SCRIPT_STEP_EVENT || SCRIPT_RUN_END_EVENTS.has(event.type))) return;
	queryClient.setQueryData<CloudCpClientEvent[]>(cloudScriptStepsQueryKey(orgId, sessionId), (current) => mergeScriptStepEvents(current ?? [], events));
}

/** Subscribes to a query's cached data without becoming an observer that could fetch it. */
function useCachedQueryData<T>(queryKey: readonly unknown[], enabled: boolean): T | undefined {
	const queryClient = useQueryClient();
	const hash = hashKey(queryKey);
	const subscribe = useCallback((notify: () => void) => queryClient.getQueryCache().subscribe((event) => {
		if (event.query.queryHash === hash) notify();
	}), [hash, queryClient]);
	const read = useCallback(() => (enabled ? queryClient.getQueryData<T>(queryKey) : undefined), [enabled, hash, queryClient]); // eslint-disable-line react-hooks/exhaustive-deps
	return useSyncExternalStore(subscribe, read, read);
}

/** The event types the step log reads; everything else is skipped server side when the control plane supports it. */
const SCRIPT_RUN_EVENT_TYPES = [SCRIPT_STEP_EVENT, ...SCRIPT_RUN_END_EVENTS] as const;

/** Control planes (by client) that refused the `types` filter: they are paged unfiltered. */
const typesFilterRefused = new WeakSet<object>();

/** A fetched log is fresh this long; a later mount reads only what came after it. */
const SCRIPT_STEPS_STALE_MS = 30_000;

/** The parts of a Cloud session's state the step log follows. */
export interface CloudScriptRunsLifecycle {
	desiredState?: string;
	observedState?: string;
	runtimeState?: string;
}

/** The session is being deleted: its Cleanup runs now, with nothing else watching. */
function cloudSessionDeleting(lifecycle: CloudScriptRunsLifecycle | undefined): boolean {
	return lifecycle?.desiredState === "deleted" && !cloudSessionScriptsEnded(lifecycle);
}

/**
 * Setup and Cleanup runs for a Cloud session, oldest first.
 *
 * Reads, in order of preference: the chat surface's event list when it is
 * loaded (it already refreshes while the chat is open), then the script-step
 * cache the startup loader fills from its live stream. Otherwise it reads the
 * session's step events: only those (the `types` filter) where the control
 * plane supports it, every event where it does not, and on a later read only
 * the events after the newest one it holds.
 *
 * It never polls. It reads again when the session's state changes (a
 * deletion starting or finishing, a startup giving up), and listens on the
 * session's event stream only while a run is in progress or the session is
 * being deleted (its Cleanup runs then), or when `live` asks for it.
 */
export function useCloudScriptRuns(
	orgId: string | undefined,
	sessionId: string | undefined,
	{ live = false, fetch = true, lifecycle }: {
		/** Listen on the session's stream regardless (the failure card after Retry). */
		live?: boolean;
		/** Off for a caller that already feeds the cache from its own stream (the startup loader). */
		fetch?: boolean;
		/** The session's state: a change reads again, and a gone sandbox ends a run that never reported its end. */
		lifecycle?: CloudScriptRunsLifecycle;
	} = {},
): ScriptRun[] {
	const { client, ready, baseUrl } = useCloudCp();
	const queryClient = useQueryClient();
	const enabled = Boolean(orgId && sessionId);
	const chatEvents = useCachedQueryData<CloudCpClientEvent[]>(cloudChatEventsQueryKey(orgId ?? "", sessionId ?? ""), enabled);
	const steps = useQuery({
		queryKey: cloudScriptStepsQueryKey(orgId ?? "", sessionId ?? ""),
		enabled: enabled && ready && fetch,
		staleTime: SCRIPT_STEPS_STALE_MS,
		refetchOnWindowFocus: false,
		queryFn: async ({ signal }) => {
			const known = queryClient.getQueryData<CloudCpClientEvent[]>(cloudScriptStepsQueryKey(orgId!, sessionId!)) ?? [];
			const chat = queryClient.getQueryData<CloudCpClientEvent[]>(cloudChatEventsQueryKey(orgId!, sessionId!));
			if (chat) return mergeScriptStepEvents(known, chat);
			// Every writer of the cache read the session from its start, so
			// nothing it lacks precedes its newest event.
			let after = known.at(-1)?.sequence ?? 0;
			let events = known;
			for (;;) {
				const filtered = !typesFilterRefused.has(client);
				let page;
				try {
					page = await client.listChatEvents(orgId!, sessionId!, { after, limit: 500, types: filtered ? SCRIPT_RUN_EVENT_TYPES : undefined }, { signal });
				} catch (error) {
					if (!filtered || !(error instanceof CloudCpError) || error.status !== 400) throw error;
					typesFilterRefused.add(client);
					continue;
				}
				events = mergeScriptStepEvents(events, page.events);
				if (!page.hasMore || page.nextAfter <= after) return events;
				after = page.nextAfter;
			}
		},
	});
	// A change in the session's state (deletion starting or done, startup
	// given up) can come with step events nothing streamed here: read again.
	const stateKey = lifecycle ? `${lifecycle.desiredState ?? ""}|${lifecycle.observedState ?? ""}|${lifecycle.runtimeState ?? ""}` : "";
	const seenStateKey = useRef(stateKey);
	useEffect(() => {
		if (seenStateKey.current === stateKey) return;
		seenStateKey.current = stateKey;
		if (enabled && fetch) void queryClient.invalidateQueries({ queryKey: cloudScriptStepsQueryKey(orgId!, sessionId!), exact: true });
	}, [enabled, fetch, orgId, queryClient, sessionId, stateKey]);
	const sessionEnded = cloudSessionScriptsEnded(lifecycle);
	const runs = useMemo(() => {
		const merged = chatEvents ? mergeScriptStepEvents(steps.data ?? [], chatEvents) : steps.data ?? [];
		return merged.length > 0 ? groupScriptRuns(merged, { sessionEnded }) : [];
	}, [chatEvents, sessionEnded, steps.data]);
	// The chat surface keeps its own list fresh; otherwise listen only while
	// something can still change.
	const inProgress = runs.some((run) => run.status === "running");
	const listen = live || (fetch && !chatEvents && steps.isSuccess && (inProgress || cloudSessionDeleting(lifecycle)));
	useEffect(() => {
		if (!listen || !enabled || !ready || !baseUrl) return;
		const controller = new AbortController();
		const after = queryClient.getQueryData<CloudCpClientEvent[]>(cloudScriptStepsQueryKey(orgId!, sessionId!))?.at(-1)?.sequence;
		void subscribeSessionEventsBridged({
			baseUrl, orgId: orgId!, sessionId: sessionId!, after, signal: controller.signal,
			onEvent: (event) => recordCloudScriptStepEvents(queryClient, orgId!, sessionId!, [event]),
		});
		return () => controller.abort();
	}, [baseUrl, enabled, listen, orgId, queryClient, ready, sessionId]);
	return runs;
}
