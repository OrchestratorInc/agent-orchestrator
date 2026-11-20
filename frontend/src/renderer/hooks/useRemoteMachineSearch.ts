import { useQueries } from "@tanstack/react-query";
import { useCallback, useMemo, useSyncExternalStore } from "react";
import { apiErrorCode } from "../lib/api-client";
import { clientForHost, labelForHost } from "../lib/host-clients";
import type { HostId } from "../lib/hosts";
import { savedRemoteHosts, subscribeSavedRemoteHosts } from "../lib/saved-remote-hosts";
import {
	boundMachineResult,
	classifyMachineSearchFailure,
	MACHINE_SEARCH_TIMEOUT_MS,
	savedMachineOutcome,
	type MachineSearchAnswer,
} from "../lib/session-search-machines";
import { useConnectedHosts } from "./useHostConnection";

/**
 * What each remote machine is asked: the same words as this machine, operators
 * included (they are parsed by each daemon).
 */
export type RemoteMachineSearchRequest = {
	query: string;
	/** Results wanted from each machine. */
	limit: number;
};

export type RemoteMachineSearch = {
	/** The words these answers are for. */
	query: string;
	/**
	 * The answers so far: every connected machine that has answered (or timed
	 * out), in the order they connected, then every saved machine that is not
	 * connected, with the reason.
	 */
	answers: MachineSearchAnswer[];
	/** Every connected machine has answered (or timed out) for this request. */
	settled: boolean;
};
const NO_HOSTS: HostId[] = [];

/**
 * Asks every connected remote AO machine the same search, each through the
 * desktop's own connection to it (the main process's remote proxy holds the
 * credentials), with its own time limit. A machine that is slow, refuses the
 * credential, runs an AO without session search, or is gone never fails the
 * search: it comes back as an answer that says why. Saved machines that are
 * not connected are reported without being asked.
 */
export function useRemoteMachineSearch(request: RemoteMachineSearchRequest, enabled: boolean): RemoteMachineSearch {
	const connected = useConnectedHosts();
	const saved = useSyncExternalStore(subscribeSavedRemoteHosts, savedRemoteHosts, savedRemoteHosts);
	const query = request.query.trim();
	const active = enabled && query.length > 0;
	const asked = active ? connected : NO_HOSTS;
	const idle = useMemo((): RemoteMachineSearch => ({ query, answers: [], settled: true }), [query]);
	// Stable while nothing it reads changes, so callers get the same answers
	// object until an answer, or the set of machines, actually changes.
	const combine = useCallback((results: { data?: MachineSearchAnswer }[]): RemoteMachineSearch => {
		if (!active) return idle;
		const answers: MachineSearchAnswer[] = [];
		let settled = true;
		asked.forEach((_, index) => {
			const result = results[index];
			if (result?.data) answers.push(result.data);
			else settled = false;
		});
		for (const host of saved) {
			if (asked.includes(host.hostId)) continue;
			answers.push({
				hostId: host.hostId,
				label: host.label || host.hostId,
				outcome: savedMachineOutcome(host),
				results: [],
				total: 0,
			});
		}
		return { query, answers, settled };
	}, [active, asked, idle, query, saved]);
	return useQueries({
		queries: asked.map((hostId) => ({
			queryKey: [
				"session-search-remote", hostId, query, request.limit,
			] as const,
			retry: false,
			staleTime: 10_000,
			queryFn: ({ signal }: { signal: AbortSignal }) => searchRemoteMachine(hostId, query, request, signal),
		})),
		combine,
	});
}

async function searchRemoteMachine(
	hostId: HostId,
	query: string,
	request: RemoteMachineSearchRequest,
	signal: AbortSignal,
): Promise<MachineSearchAnswer> {
	const label = labelForHost(hostId) ?? hostId;
	const failed = (outcome: MachineSearchAnswer["outcome"]): MachineSearchAnswer => ({ hostId, label, outcome, results: [], total: 0 });
	// The query layer's signal (a newer query replaced this one) and this
	// machine's time limit, as one.
	const controller = new AbortController();
	let timedOut = false;
	const timer = setTimeout(() => {
		timedOut = true;
		controller.abort();
	}, MACHINE_SEARCH_TIMEOUT_MS);
	const cancel = () => controller.abort();
	signal.addEventListener("abort", cancel, { once: true });
	try {
		const { data, error, response } = await clientForHost(hostId).GET("/api/v1/search/sessions", {
			signal: controller.signal,
			params: {
				query: { q: query, limit: request.limit },
			},
		});
		if (!response.ok || !data) return failed(classifyMachineSearchFailure(response.status, undefined, apiErrorCode(error)));
		// Another machine's AO may be older, newer or broken: take no more rows
		// than were asked for, each bounded before the palette shows it.
		const results = (Array.isArray(data.results) ? data.results : []).slice(0, request.limit).map(boundMachineResult);
		return { hostId, label, outcome: "searched", results, total: typeof data.total === "number" ? data.total : results.length, sort: data.sort };
	} catch (error) {
		// Replaced by a newer query: let the query layer drop this one.
		if (signal.aborted) throw error;
		return failed(timedOut ? "timed_out" : classifyMachineSearchFailure(undefined, error));
	} finally {
		clearTimeout(timer);
		signal.removeEventListener("abort", cancel);
	}
}
