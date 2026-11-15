import { hashKey, useQuery, useQueryClient, type QueryClient } from "@tanstack/react-query";
import { useCallback, useEffect, useMemo, useRef, useSyncExternalStore } from "react";
import { CloudCpError, type CloudCpClientEvent } from "../lib/cloud-cp";
import { cloudChatHistoryPartial } from "../lib/cloud-chat-history";
import { cloudSessionScriptsEnded, groupScriptRuns, mergeScriptStepEvents, SCRIPT_RUN_END_EVENTS, SCRIPT_STEP_EVENT, type ScriptRun } from "../lib/cloud-script-runs";
import { useCloudCp } from "./useCloudCp";
import { useCloudSessionEventListener } from "./useCloudSessionEvents";

export function cloudScriptStepsQueryKey(orgId: string, sessionId: string) {
	return ["cloud-script-steps", orgId, sessionId] as const;
}

export function cloudChatEventsQueryKey(orgId: string, sessionId: string) {
	return ["cloud-chat-events", orgId, sessionId] as const;
}

export function recordCloudScriptStepEvents(queryClient: QueryClient, orgId: string, sessionId: string, events: readonly CloudCpClientEvent[]) {
	if (!orgId || !events.some((event) => event.type === SCRIPT_STEP_EVENT || SCRIPT_RUN_END_EVENTS.has(event.type))) return;
	queryClient.setQueryData<CloudCpClientEvent[]>(cloudScriptStepsQueryKey(orgId, sessionId), (current) => mergeScriptStepEvents(current ?? [], events));
}

export function useCachedQueryData<T>(queryKey: readonly unknown[], enabled: boolean): T | undefined {
	const queryClient = useQueryClient();
	const hash = hashKey(queryKey);
	const subscribe = useCallback((notify: () => void) => queryClient.getQueryCache().subscribe((event) => {
		if (event.query.queryHash === hash) notify();
	}), [hash, queryClient]);
	const read = useCallback(() => (enabled ? queryClient.getQueryData<T>(queryKey) : undefined), [enabled, hash, queryClient]); // eslint-disable-line react-hooks/exhaustive-deps
	return useSyncExternalStore(subscribe, read, read);
}

async function chatEventsInFlight(queryClient: QueryClient, queryKey: readonly unknown[], signal: AbortSignal): Promise<CloudCpClientEvent[] | undefined> {
	await new Promise((resolve) => setTimeout(resolve, 0));
	const cache = queryClient.getQueryCache();
	const query = cache.find<CloudCpClientEvent[]>({ queryKey, exact: true });
	if (!query || signal.aborted) return undefined;
	if (query.state.data !== undefined || query.state.fetchStatus !== "fetching") return query.state.data;
	return new Promise((resolve) => {
		const done = (events: CloudCpClientEvent[] | undefined) => {
			unsubscribe();
			signal.removeEventListener("abort", stop);
			resolve(events);
		};
		const stop = () => done(undefined);
		const unsubscribe = cache.subscribe((event) => {
			if (event.query !== query) return;
			if (query.state.data !== undefined) done(query.state.data);
			else if (query.state.fetchStatus !== "fetching") done(undefined);
		});
		signal.addEventListener("abort", stop, { once: true });
	});
}

const SCRIPT_RUN_EVENT_TYPES = [SCRIPT_STEP_EVENT, ...SCRIPT_RUN_END_EVENTS] as const;

const typesFilterRefused = new WeakSet<object>();

const SCRIPT_STEPS_STALE_MS = 30_000;

export interface CloudScriptRunsLifecycle {
	desiredState?: string;
	observedState?: string;
	runtimeState?: string;
}

function cloudSessionDeleting(lifecycle: CloudScriptRunsLifecycle | undefined): boolean {
	return lifecycle?.desiredState === "deleted" && !cloudSessionScriptsEnded(lifecycle);
}

export function useCloudScriptRuns(
	orgId: string | undefined,
	sessionId: string | undefined,
	{ live = false, fetch = true, lifecycle }: {
		live?: boolean;
		fetch?: boolean;
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
			const chatKey = cloudChatEventsQueryKey(orgId!, sessionId!);
			const chat = queryClient.getQueryData<CloudCpClientEvent[]>(chatKey) ?? await chatEventsInFlight(queryClient, chatKey, signal);
			if (chat && !cloudChatHistoryPartial(queryClient, orgId!, sessionId!)) return mergeScriptStepEvents(known, chat);
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
	const inProgress = runs.some((run) => run.status === "running");
	const listen = live || (fetch && !chatEvents && steps.isSuccess && (inProgress || cloudSessionDeleting(lifecycle)));
	useCloudSessionEventListener({
		baseUrl,
		orgId,
		sessionId,
		enabled: listen && enabled && ready,
		onEvent: (event) => recordCloudScriptStepEvents(queryClient, orgId!, sessionId!, [event]),
		resumeAfter: () => queryClient.getQueryData<CloudCpClientEvent[]>(cloudScriptStepsQueryKey(orgId!, sessionId!))?.at(-1)?.sequence ?? 0,
	});
	return runs;
}
