import { useEffect, useMemo, useRef, useState } from "react";
import { LOCAL_HOST } from "../lib/hosts";
import {
	isQueryRefinement,
	MACHINE_SEARCH_GATHER_MS,
	mergeNewestFirst,
	orderMachineSearchResults,
	pickMachineSearchRows,
	type MachineSearchAnswer,
	type MachineSearchResult,
} from "../lib/session-search-machines";
import type { SessionSearchResponse } from "./usePaletteSessionSearch";
import type { RemoteMachineSearch } from "./useRemoteMachineSearch";

export type MachineSearchRowsInput = {
	/** The words searched; empty for recents. */
	query: string;
	/** This machine's answer for `query`; undefined until it lands (a placeholder is not an answer). */
	local: SessionSearchResponse | undefined;
	/** This machine's search for `query` finished and failed (its index is off or could not open). */
	localFailed: boolean;
	remote: RemoteMachineSearch;
	/** Rows wanted when one machine answers; more machines can add one row each. */
	limit: number;
};

export type MachineSearchRows = {
	rows: MachineSearchResult[];
	/** False while the rows are an earlier query's, kept until this one's land. */
	current: boolean;
};

type Shown = { query: string; rows: MachineSearchResult[] };

/**
 * The session rows the palette shows for a search across machines.
 *
 * This machine answers first and usually fast; remote machines answer when they
 * do. The list waits for whichever comes first: every machine has answered, or
 * MACHINE_SEARCH_GATHER_MS has passed since this machine did. Machines that
 * answered by then are ranked in together. A machine that answers later (up to
 * its six-second limit) adds its rows below the ones on screen, so a slow
 * machine neither holds the list back nor moves the row under the cursor.
 *
 * Until this query's list is ready, the previous list stays only when this
 * query refines it ("orb" then "orbit"); an unrelated query shows no rows
 * rather than ones Enter would open by mistake.
 */
export function useMachineSearchRows({ query, local, localFailed, remote, limit }: MachineSearchRowsInput): MachineSearchRows {
	const localDone = local !== undefined || localFailed;
	// Remote answers for an earlier query are not this query's.
	const remoteForQuery = remote.query === query;
	const remoteSettled = remoteForQuery && remote.settled;
	// Each change of query starts its own wait, so typing back to an earlier
	// query waits again rather than reusing that query's finished one.
	const episode = useMemo(() => ({ query }), [query]);
	const [gatherEndedFor, setGatherEndedFor] = useState<{ query: string } | null>(null);
	useEffect(() => {
		if (!localDone || remoteSettled) return;
		const timer = window.setTimeout(() => setGatherEndedFor(episode), MACHINE_SEARCH_GATHER_MS);
		return () => window.clearTimeout(timer);
	}, [episode, localDone, remoteSettled]);
	const ready = localDone && (remoteSettled || gatherEndedFor === episode);

	// What was on screen at the last commit, and for which query.
	const shownRef = useRef<Shown>({ query: "", rows: [] });
	const result = useMemo((): MachineSearchRows & { query: string } => {
		const shown = shownRef.current;
		if (!ready) {
			const keep = shown.query === query || isQueryRefinement(shown.query, query);
			return { query: shown.query, rows: keep ? shown.rows : [], current: false };
		}
		const answers: MachineSearchAnswer[] = [];
		if (local) {
			answers.push({ hostId: LOCAL_HOST, label: "", outcome: "searched", results: local.results ?? [], total: local.total ?? 0 });
		}
		if (remoteForQuery) answers.push(...remote.answers.filter((answer) => answer.outcome === "searched"));
		// A failed local search still follows the order a remote applied.
		const sortedBy = local ?? answers.find((answer) => answer.sort);
		const ordered = orderMachineSearchResults(answers, mergeNewestFirst(sortedBy));
		const rows = pickMachineSearchRows(ordered, limit, shown.query === query ? shown.rows : []);
		return { query, rows, current: true };
	}, [limit, local, query, ready, remote.answers, remoteForQuery]);

	useEffect(() => {
		shownRef.current = { query: result.query, rows: result.rows };
	}, [result]);

	return { rows: result.rows, current: result.current };
}
