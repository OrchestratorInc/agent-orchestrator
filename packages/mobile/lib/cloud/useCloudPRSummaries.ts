import { useCallback, useEffect, useRef, useState } from "react";
import type { SessionPRSummary } from "../api";
import type { CloudClient } from "./client";
import { useCloud } from "./CloudProvider";
import { cloudPRSummary } from "./pullRequests";
import { forgetOnCloudAccountChange } from "./accountCaches";

// The cloud half of usePRSummaries: a cloud session's pull-request detail
// (title, branches, diff size, CI and review) from the control plane, fetched
// once per visit and reused across tabs for a minute, as the paired machines'
// is. The board's session list carries PR facts only. A target's stamp (its
// pull requests' states, say) is part of what is reused: a pull request merged
// or closed in Cloud reads afresh instead of keeping its open card for a minute.

const BATCH = 6;
const REUSE_MS = 60_000;
const MAX_ENTRIES = 256;
const retained = new Map<string, { expiresAt: number; summaries: SessionPRSummary[] }>();

function cacheKey(client: CloudClient, orgId: string, sessionId: string, stamp = ""): string {
	return JSON.stringify([client.baseUrl, orgId, sessionId, stamp]);
}

/** A session whose pull requests to read, and what its reused detail must still match. */
export type CloudPRSummaryTarget = string | { sessionId: string; stamp?: string };

function retainedSummaries(key: string): SessionPRSummary[] | undefined {
	const entry = retained.get(key);
	if (!entry) return undefined;
	if (entry.expiresAt > Date.now()) return entry.summaries;
	retained.delete(key);
	return undefined;
}

function retain(key: string, summaries: SessionPRSummary[]) {
	retained.delete(key);
	retained.set(key, { expiresAt: Date.now() + REUSE_MS, summaries });
	if (retained.size > MAX_ENTRIES) retained.delete(retained.keys().next().value!);
}

/** Forget what was retained; for tests and sign-out. */
export function clearCloudPRSummaries() {
	retained.clear();
}
forgetOnCloudAccountChange(clearCloudPRSummaries);

export type CloudPRSummaryLookup = {
	summaryFor(sessionId: string, number: number): SessionPRSummary | undefined;
	/** Fetch everything again: pull-to-refresh. */
	reload(): void;
};

export function useCloudPRSummaries(targetList: readonly CloudPRSummaryTarget[]): CloudPRSummaryLookup {
	const { client, org, status } = useCloud();
	const orgId = status === "ready" ? org?.id : undefined;
	const cache = useRef<Record<string, SessionPRSummary[]>>({});
	const inFlight = useRef(new Set<string>());
	const [, bump] = useState(0);
	const [generation, setGeneration] = useState(0);
	const fetchedGeneration = useRef(0);
	const entries = new Map(targetList.map((target) => typeof target === "string" ? [target, ""] : [target.sessionId, target.stamp ?? ""]));
	const ids = JSON.stringify([...entries].sort(([a], [b]) => a.localeCompare(b)));
	const stamps = useRef(entries);
	stamps.current = entries;

	useEffect(() => {
		if (!orgId) return;
		const force = fetchedGeneration.current !== generation;
		fetchedGeneration.current = generation;
		const targets = (JSON.parse(ids) as [string, string][]).filter(([sessionId, stamp]) => {
			const key = cacheKey(client, orgId, sessionId, stamp);
			if (!force && cache.current[key] === undefined) {
				const kept = retainedSummaries(key);
				if (kept) cache.current[key] = kept;
			}
			return !inFlight.current.has(key) && (force || cache.current[key] === undefined);
		});
		if (targets.length === 0) return;
		const keys = targets.map(([sessionId, stamp]) => cacheKey(client, orgId, sessionId, stamp));
		for (const key of keys) inFlight.current.add(key);
		let cancelled = false;
		void (async () => {
			for (let index = 0; index < targets.length; index += BATCH) {
				if (cancelled) return;
				const chunk = targets.slice(index, index + BATCH);
				const results = await Promise.all(chunk.map(async ([sessionId]) => {
					try {
						const { pullRequests } = await client.listSessionPullRequests(orgId, sessionId);
						return pullRequests.map(cloudPRSummary);
					} catch {
						return null;
					}
				}));
				if (cancelled) return;
				results.forEach((summaries, offset) => {
					const key = keys[index + offset]!;
					// A failed refresh keeps what is on screen rather than thinning the cards.
					if (summaries) {
						cache.current[key] = summaries;
						retain(key, summaries);
					} else cache.current[key] ??= [];
					inFlight.current.delete(key);
				});
				bump((count) => count + 1);
			}
		})();
		return () => {
			cancelled = true;
			for (const key of keys) inFlight.current.delete(key);
		};
	}, [client, generation, ids, orgId]);

	const reload = useCallback(() => setGeneration((count) => count + 1), []);
	const summaryFor = useCallback((sessionId: string, number: number) => {
		if (!orgId) return undefined;
		const key = cacheKey(client, orgId, sessionId, stamps.current.get(sessionId));
		return (cache.current[key] ?? retainedSummaries(key))?.find((pr) => pr.number === number);
	}, [client, orgId]);
	return { summaryFor, reload };
}
