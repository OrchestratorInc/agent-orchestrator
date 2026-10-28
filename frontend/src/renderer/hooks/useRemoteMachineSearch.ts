import { useQueries } from "@tanstack/react-query";
import { useCallback, useSyncExternalStore } from "react";
import { clientForHost, labelForHost } from "../lib/host-clients";
import type { HostId } from "../lib/hosts";
import { savedRemoteHosts, subscribeSavedRemoteHosts } from "../lib/saved-remote-hosts";
import {
	classifyMachineSearchFailure,
	MACHINE_SEARCH_TIMEOUT_MS,
	type MachineSearchAnswer,
} from "../lib/session-search-machines";
import { useConnectedHosts } from "./useHostConnection";
import type { SessionSearchFilters } from "./useSessionSearch";

/**
 * What each remote machine is asked: the same words and filters as this
 * machine. Never a project: a project id names a project on one machine.
 */
export type RemoteMachineSearchRequest = Pick<SessionSearchFilters, "query" | "harness" | "from" | "to" | "sort"> & {
	/** Results wanted from each machine: everything up to the end of the page. */
	limit: number;
};

export type RemoteMachineSearch = {
	/** One answer per saved remote machine, in the order they were saved. */
	answers: MachineSearchAnswer[];
	/** Every connected machine has answered (or timed out) for this request. */
	settled: boolean;
};

const NO_ANSWERS: RemoteMachineSearch = { answers: [], settled: true };
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
	// Stable while nothing it reads changes, so callers get the same answers
	// object until an answer, or the set of machines, actually changes.
	const combine = useCallback((results: { data?: MachineSearchAnswer }[]): RemoteMachineSearch => {
		if (!active) return NO_ANSWERS;
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
				outcome: host.status === "connecting" ? "connecting" : "offline",
				results: [],
				total: 0,
			});
		}
		return { answers, settled };
	}, [active, asked, saved]);
	return useQueries({
		queries: asked.map((hostId) => ({
			queryKey: [
				"session-search-remote", hostId, query, request.harness, request.from, request.to,
				request.sort ?? "relevance", request.limit,
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
		const { data, response } = await clientForHost(hostId).GET("/api/v1/search/sessions", {
			signal: controller.signal,
			params: {
				query: {
					q: query,
					...(request.harness ? { harness: request.harness } : {}),
					...(request.from ? { from: request.from } : {}),
					...(request.to ? { to: request.to } : {}),
					...(request.sort ? { sort: request.sort } : {}),
					limit: request.limit,
				},
			},
		});
		if (!response.ok || !data) return failed(classifyMachineSearchFailure(response.status));
		const results = data.results ?? [];
		return { hostId, label, outcome: "searched", results, total: data.total ?? results.length };
	} catch (error) {
		// Replaced by a newer query: let the query layer drop this one.
		if (signal.aborted) throw error;
		return failed(timedOut ? "timed_out" : classifyMachineSearchFailure(undefined, error));
	} finally {
		clearTimeout(timer);
		signal.removeEventListener("abort", cancel);
	}
}
