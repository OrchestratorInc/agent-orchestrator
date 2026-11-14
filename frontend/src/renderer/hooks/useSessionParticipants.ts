import { useQueries } from "@tanstack/react-query";
import { useMemo } from "react";
import type { CloudCpSessionParticipant } from "../lib/cloud-cp";
import type { WorkspaceSession } from "../types/workspace";
import { useCloudCp } from "./useCloudCp";

/** How often a board re-reads who is in its shared sessions. */
export const SESSION_PARTICIPANTS_REFRESH_MS = 3_000;
const MAX_PER_REQUEST = 100;

/**
 * Who is in each shared cloud session on a board, keyed by session id. One
 * request per organization (and per hundred sessions); sessions nobody shared
 * are absent. Reading it never marks this person present in a session.
 */
export function useSessionParticipants(sessions: readonly WorkspaceSession[], enabled = true): ReadonlyMap<string, CloudCpSessionParticipant[]> {
	const batches = useMemo(() => {
		if (!enabled) return [];
		const byOrg = new Map<string, string[]>();
		for (const session of sessions) {
			const orgId = session.cloud?.orgId;
			if (!orgId || session.isTerminated === true) continue;
			const ids = byOrg.get(orgId) ?? [];
			ids.push(session.id);
			byOrg.set(orgId, ids);
		}
		const result: { orgId: string; sessionIds: string[] }[] = [];
		for (const [orgId, ids] of byOrg) {
			const sorted = [...ids].sort();
			for (let start = 0; start < sorted.length; start += MAX_PER_REQUEST) {
				result.push({ orgId, sessionIds: sorted.slice(start, start + MAX_PER_REQUEST) });
			}
		}
		return result;
	}, [enabled, sessions]);
	const { client, baseUrl, ready } = useCloudCp(batches.length > 0);
	return useQueries({
		queries: batches.map((batch) => ({
			queryKey: ["cloud-session-participants", baseUrl, batch.orgId, batch.sessionIds.join(",")],
			enabled: ready,
			refetchInterval: SESSION_PARTICIPANTS_REFRESH_MS,
			retry: 1,
			queryFn: async ({ signal }: { signal: AbortSignal }) => (await client.listSessionParticipants(batch.orgId, batch.sessionIds, { signal })).sessions,
		})),
		combine: combineParticipants,
	});
}

function combineParticipants(results: { data?: { sessionId: string; participants: CloudCpSessionParticipant[] }[] }[]): ReadonlyMap<string, CloudCpSessionParticipant[]> {
	const map = new Map<string, CloudCpSessionParticipant[]>();
	for (const result of results) {
		for (const entry of result.data ?? []) map.set(entry.sessionId, entry.participants);
	}
	return map;
}
