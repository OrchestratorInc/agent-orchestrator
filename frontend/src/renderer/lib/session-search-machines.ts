import type { TFunction } from "i18next";
import type { components } from "../../api/schema";
import { appI18n } from "../i18n";
import { LOCAL_HOST, type HostId } from "./hosts";

/**
 * Search across machines: this machine's daemon and every connected remote AO
 * machine are asked the same query, and their ranked lists are merged here.
 *
 * The desktop does the fan-out because it is where every machine meets: it
 * already holds each remote's saved credentials (the main process's remote
 * proxy), so no daemon reads another machine's password.
 */

type SessionSearchResult = components["schemas"]["SessionSearchResult"];

/** How long a remote machine has to answer before it is reported as slow. */
export const MACHINE_SEARCH_TIMEOUT_MS = 6_000;

/** What happened to one machine in a search across machines. */
export type MachineSearchOutcome =
	| "searched"
	/** Saved, but its connection is down. */
	| "offline"
	/** Saved, and its connection is still being made. */
	| "connecting"
	| "timed_out"
	/** Its AO has no session search (an older version). */
	| "needs_update"
	/** Its saved credential was refused. */
	| "unauthorized"
	| "failed";

/** One machine's answer, or the reason there is none. */
export type MachineSearchAnswer = {
	/** LOCAL_HOST for this machine. */
	hostId: HostId;
	label: string;
	outcome: MachineSearchOutcome;
	results: readonly SessionSearchResult[];
	total: number;
};

/** A result as the desktop shows it: stamped with the machine it is on. */
export type MachineSearchResult = SessionSearchResult & {
	/** The remote machine the session is on; absent for this machine. */
	hostId?: HostId;
	/** That machine's name, shown on the row. */
	machineLabel?: string;
};

/**
 * Merges the machines' ranked lists into one.
 *
 * Relevance scores are not compared: bm25 depends on each machine's own index,
 * so a 3.2 on one machine and a 3.2 on another mean different things. As Orca
 * does, the lists take turns instead: every machine's best, then every
 * machine's second, and so on, this machine first.
 *
 * One rule comes before taking turns. A session whose name holds every typed
 * word (namedForQuery, judged from the name and the query alone) means the
 * same thing on every machine, so those sessions come first, in turns, and the
 * rest follow, in turns. Plain turn-taking put a passing mention on one
 * machine above five sessions called "Morning standup" on another.
 *
 * Sorted newest first, there is nothing to weigh: the lists merge by time.
 */
export function mergeMachineSearchResults(
	answers: readonly MachineSearchAnswer[],
	newestFirst: boolean,
): MachineSearchResult[] {
	const lists = answers.map((answer) =>
		answer.results.map((result): MachineSearchResult =>
			answer.hostId === LOCAL_HOST ? { ...result } : { ...result, hostId: answer.hostId, machineLabel: answer.label },
		),
	);
	if (newestFirst) {
		// Stable: on equal times a machine earlier in the list stays first.
		return lists
			.flatMap((list, machine) => list.map((result, position) => ({ result, machine, position })))
			.sort((left, right) =>
				Date.parse(right.result.createdAt) - Date.parse(left.result.createdAt)
				|| left.machine - right.machine
				|| left.position - right.position)
			.map((entry) => entry.result);
	}
	const named = lists.map((list) => list.filter((result) => result.namedForQuery));
	const rest = lists.map((list) => list.filter((result) => !result.namedForQuery));
	return [...takeTurns(named), ...takeTurns(rest)];
}

/** Each list's first, then each list's second, and so on, in list order. */
function takeTurns<T>(lists: readonly (readonly T[])[]): T[] {
	const merged: T[] = [];
	const longest = Math.max(0, ...lists.map((list) => list.length));
	for (let depth = 0; depth < longest; depth++) {
		for (const list of lists) {
			if (depth < list.length) merged.push(list[depth]!);
		}
	}
	return merged;
}

/**
 * Names why a remote machine did not answer.
 *
 * A tunnel or proxy in front of a machine answers 502, 504, 520-527 or 530 when
 * the machine behind it is gone (a quick tunnel whose machine stopped answers
 * 530), and a request that cannot connect at all throws: both are "offline".
 */
export function classifyMachineSearchFailure(status: number | undefined, error?: unknown): MachineSearchOutcome {
	if (status === undefined) {
		if (error instanceof DOMException && (error.name === "TimeoutError" || error.name === "AbortError")) return "timed_out";
		if (error instanceof TypeError) return "offline";
		return "failed";
	}
	if (status === 401 || status === 403) return "unauthorized";
	if (status === 404 || status === 405 || status === 501) return "needs_update";
	if (status === 502 || status === 504 || (status >= 520 && status <= 527) || status === 530) return "offline";
	return "failed";
}

/** The reason shown after a machine's name in "Not searched on …". */
export function machineSearchOutcomeLabel(outcome: MachineSearchOutcome, t: TFunction = appI18n.t): string {
	switch (outcome) {
		case "offline":
			return t("search.machineOffline");
		case "connecting":
			return t("search.machineConnecting");
		case "timed_out":
			return t("search.machineTimedOut", { seconds: MACHINE_SEARCH_TIMEOUT_MS / 1000 });
		case "needs_update":
			return t("search.machineNeedsUpdate");
		case "unauthorized":
			return t("search.machineUnauthorized");
		case "failed":
			return t("search.machineFailed");
		case "searched":
			return "";
	}
}

/** "Laptop (offline), Old desk (update AO there)", for every machine not searched. */
export function machinesNotSearched(answers: readonly Pick<MachineSearchAnswer, "label" | "outcome">[], t: TFunction = appI18n.t): string[] {
	return answers
		.filter((answer) => answer.outcome !== "searched")
		.map((answer) => `${answer.label} (${machineSearchOutcomeLabel(answer.outcome, t)})`);
}
