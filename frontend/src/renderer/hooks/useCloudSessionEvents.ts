import { useCallback, useEffect, useRef, useSyncExternalStore } from "react";
import type { CloudCpClientEvent } from "../lib/cloud-cp";
import {
	cloudSessionEventsState,
	subscribeCloudSessionEvents,
	type CloudSessionEventsResume,
	type CloudSessionEventsState,
} from "../lib/cloud-cp/session-events-hub";

export interface CloudSessionEventsOptions {
	baseUrl: string;
	orgId: string | undefined;
	sessionId: string | undefined;
	enabled: boolean;
	onEvent?: (event: CloudCpClientEvent) => void;
	resumeAfter?: () => CloudSessionEventsResume;
}

function useLatest<T>(value: T) {
	const ref = useRef(value);
	ref.current = value;
	return ref;
}

export function useCloudSessionEventListener({ baseUrl, orgId, sessionId, enabled, onEvent, resumeAfter }: CloudSessionEventsOptions): void {
	const onEventRef = useLatest(onEvent);
	const resumeRef = useLatest(resumeAfter);
	const active = Boolean(enabled && baseUrl && orgId && sessionId);
	useEffect(() => {
		if (!active) return;
		return subscribeCloudSessionEvents({ baseUrl, orgId: orgId!, sessionId: sessionId! }, {
			onEvent: (event) => onEventRef.current?.(event),
			resumeAfter: () => resumeRef.current?.(),
		});
	}, [active, baseUrl, onEventRef, orgId, resumeRef, sessionId]);
}

function useCloudSessionEventsSnapshot<T>(
	{ baseUrl, orgId, sessionId, enabled, onEvent, resumeAfter, live = false }: CloudSessionEventsOptions & { live?: boolean },
	pick: (state: CloudSessionEventsState) => T,
): T {
	const onEventRef = useLatest(onEvent);
	const resumeRef = useLatest(resumeAfter);
	const listens = onEvent !== undefined;
	const active = Boolean(enabled && baseUrl && orgId && sessionId);
	const subscribe = useCallback((notify: () => void) => {
		if (!active) return () => undefined;
		return subscribeCloudSessionEvents({ baseUrl, orgId: orgId!, sessionId: sessionId! }, {
			live,
			onEvent: listens ? (event) => onEventRef.current?.(event) : undefined,
			resumeAfter: () => resumeRef.current?.(),
			onStateChange: notify,
		});
	}, [active, baseUrl, listens, live, onEventRef, orgId, resumeRef, sessionId]);
	const snapshot = useCallback(
		() => pick(active ? cloudSessionEventsState({ baseUrl, orgId: orgId!, sessionId: sessionId! }) : cloudSessionEventsState({ baseUrl: "", orgId: "", sessionId: "" })),
		[active, baseUrl, orgId, pick, sessionId],
	);
	return useSyncExternalStore(subscribe, snapshot, snapshot);
}

const pickOpen = (state: CloudSessionEventsState) => state.open;
const pickLive = (state: CloudSessionEventsState) => state.live;

export function useCloudSessionEventStream(options: CloudSessionEventsOptions): boolean {
	return useCloudSessionEventsSnapshot(options, pickOpen);
}

export function useCloudSessionLiveState(options: Omit<CloudSessionEventsOptions, "onEvent" | "resumeAfter">) {
	return useCloudSessionEventsSnapshot({ ...options, live: true }, pickLive);
}
