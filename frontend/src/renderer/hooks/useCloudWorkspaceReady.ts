import { useCallback, useEffect, useSyncExternalStore } from "react";
import { cloudSessionStartRequestInFlight, cloudWorkerAnswering, subscribeCloudSessionStartRequests } from "../lib/cloud-lifecycle";
import { cloudWorkerStartingWait, forgetCloudWorkerStarting, subscribeCloudWorkerStarting } from "../lib/cloud-worker-starting";
import type { WorkspaceSession } from "../types/workspace";

/**
 * Whether a Cloud session's workspace reads (review, Chat models, prompt
 * suggestions) may be sent: its worker is connected and running, and it has
 * not just said it is still preparing the workspace (cloud-worker-starting).
 * Until then the callers show their loading state and send nothing.
 */
export function useCloudWorkspaceReady(session: WorkspaceSession | undefined): boolean {
	const orgId = session?.cloud?.orgId;
	const sessionId = session?.id;
	const answering = cloudWorkerAnswering(session);
	const waiting = useSyncExternalStore(
		subscribeCloudWorkerStarting,
		useCallback(() => Boolean(orgId && sessionId && cloudWorkerStartingWait(orgId, sessionId)), [orgId, sessionId]),
	);
	// A worker that went away takes its backoff with it: the next one starts afresh.
	useEffect(() => {
		if (orgId && sessionId && !answering) forgetCloudWorkerStarting(orgId, sessionId);
	}, [answering, orgId, sessionId]);
	return answering && !waiting;
}

/** Whether a resume, workspace checkout or restore of the session is in flight, so the controls that send one wait. */
export function useCloudSessionStartRequestInFlight(orgId: string | undefined, sessionId: string | undefined): boolean {
	return useSyncExternalStore(
		subscribeCloudSessionStartRequests,
		useCallback(() => Boolean(orgId && sessionId && cloudSessionStartRequestInFlight(orgId, sessionId)), [orgId, sessionId]),
	);
}
