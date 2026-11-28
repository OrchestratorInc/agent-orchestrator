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
type SessionSearchHighlight = components["schemas"]["SessionSearchHighlight"];

/** How long a remote machine has to answer before it is reported as slow. */
export const MACHINE_SEARCH_TIMEOUT_MS = 6_000;

/**
 * How long, after this machine has answered, the list waits for the remote
 * machines before it shows. Machines that answer within it are ranked in with
 * this machine's rows; one that answers later is added below the rows already
 * on screen, so a slow machine never holds the list back for long and never
 * moves a row the person is about to press.
 */
export const MACHINE_SEARCH_GATHER_MS = 400;

/**
 * The longest snippet a remote machine's row shows. A daemon's own snippets
 * are a dozen or so words; this bounds what a misbehaving machine can put in
 * the palette.
 */
export const MACHINE_SNIPPET_MAX_CHARS = 300;

/** Context kept before the first match when a remote snippet is cut. */
const MACHINE_SNIPPET_LEAD_CHARS = 40;

/** The longest session name a remote machine's row carries. */
const MACHINE_SESSION_NAME_MAX_CHARS = 300;

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
	/** Its AO has session search, turned off or unable to open its index. */
	| "search_off"
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
	/** The order the machine applied (a typed sort:recent), when it answered. */
	sort?: string;
};

/** A result as the desktop shows it: stamped with the machine it is on. */
export type MachineSearchResult = SessionSearchResult & {
	/** The remote machine the session is on; absent for this machine. */
	hostId?: HostId;
	/** That machine's name, shown on the row. */
	machineLabel?: string;
};

/**
 * Whether machines' lists merge newest first: when this machine's daemon sorted
 * by time, as a typed `sort:recent` asks.
 */
export function mergeNewestFirst(local: { sort?: string } | undefined): boolean {
	return local?.sort === "recent";
}

function machineOf(result: MachineSearchResult): HostId {
	return result.hostId ?? LOCAL_HOST;
}

/** One row per session per machine: the same session id can exist on two machines. */
export function machineResultKey(result: MachineSearchResult): string {
	return `${machineOf(result)}\u0000${result.sessionId}`;
}

/** Milliseconds since the epoch, or -Infinity for a missing or unreadable time (oldest). */
function timeOf(result: SessionSearchResult): number {
	const time = Date.parse(result.createdAt);
	return Number.isNaN(time) ? Number.NEGATIVE_INFINITY : time;
}

/**
 * Orders every answered machine's rows into one list, before any is cut.
 *
 * Relevance scores are not compared: bm25 depends on each machine's own index,
 * so a 3.2 on one machine and a 3.2 on another mean different things.
 *
 * A session whose name holds every typed word (namedForQuery, judged from the
 * name and the query alone) means the same thing on every machine, so those
 * sessions come first, and the rest follow. Within each of the two, every
 * machine's best comes first, this machine first and the others in the order
 * they were saved; the remaining rows then go to the machines in proportion to
 * how many sessions matched on each. A machine where 80 sessions mention the
 * words gets more of the list than one where 3 do, and none is left out for
 * having fewer.
 *
 * Sorted newest first there is nothing to weigh: the lists merge by time, and a
 * row with no readable time sorts last.
 */
export function orderMachineSearchResults(
	answers: readonly MachineSearchAnswer[],
	newestFirst: boolean,
): MachineSearchResult[] {
	const answered = answers.filter((answer) => answer.results.length > 0);
	const lists = answered.map((answer) =>
		answer.results.map((result): MachineSearchResult =>
			answer.hostId === LOCAL_HOST ? { ...result } : { ...result, hostId: answer.hostId, machineLabel: answer.label },
		),
	);
	if (newestFirst) {
		// Stable: on equal times a machine earlier in the list stays first.
		return lists
			.flatMap((list, machine) => list.map((result, position) => ({ result, machine, position })))
			.sort((left, right) =>
				compareTimesNewestFirst(timeOf(left.result), timeOf(right.result))
				|| left.machine - right.machine
				|| left.position - right.position)
			.map((entry) => entry.result);
	}
	const weights = answered.map((answer) => Math.max(answer.total, answer.results.length, 1));
	const named = lists.map((list) => list.filter((result) => result.namedForQuery));
	const rest = lists.map((list) => list.filter((result) => !result.namedForQuery));
	return [...bestThenShare(named, weights), ...bestThenShare(rest, weights)];
}

/** Newest first; two missing times are equal rather than NaN. */
function compareTimesNewestFirst(left: number, right: number): number {
	if (left === right) return 0;
	return right > left ? 1 : -1;
}

/**
 * Every list's first, in list order; then the remaining rows shared by weight
 * (smooth weighted round-robin: deterministic, and with equal weights it is
 * plain turn-taking).
 */
function bestThenShare<T>(lists: readonly (readonly T[])[], weights: readonly number[]): T[] {
	const merged: T[] = [];
	const lanes = lists.map((list, index) => ({ list, weight: weights[index] ?? 1, next: 1, credit: 0 }));
	for (const lane of lanes) if (lane.list.length > 0) merged.push(lane.list[0] as T);
	for (;;) {
		const open = lanes.filter((lane) => lane.next < lane.list.length);
		const [first] = open;
		if (!first) return merged;
		const total = open.reduce((sum, lane) => sum + lane.weight, 0);
		let chosen = first;
		for (const lane of open) {
			lane.credit += lane.weight;
			if (lane.credit > chosen.credit) chosen = lane;
		}
		chosen.credit -= total;
		merged.push(chosen.list[chosen.next] as T);
		chosen.next += 1;
	}
}

/**
 * The rows to show, from the ordered list.
 *
 * At most `limit` rows, unless more machines than that have matches: every
 * machine with a match gets at least one row, so the list grows to one row per
 * such machine rather than leaving the last ones out.
 *
 * `shown` is what is on screen for this same query. Those rows stay, in their
 * places, and anything new goes below them: a machine that answers late adds
 * its row at the end instead of moving the row under the cursor. A shown row
 * the latest answers no longer hold is dropped, and the rest take the latest
 * answers' data.
 */
export function pickMachineSearchRows(
	ordered: readonly MachineSearchResult[],
	limit: number,
	shownBefore: readonly MachineSearchResult[] = [],
): MachineSearchResult[] {
	const latest = new Map(ordered.map((row) => [machineResultKey(row), row]));
	const shown = shownBefore.flatMap((row) => {
		const current = latest.get(machineResultKey(row));
		return current ? [current] : [];
	});
	const machines = new Set(ordered.map(machineOf));
	const budget = Math.max(limit, machines.size);
	const picked = [...shown];
	const keys = new Set(shown.map(machineResultKey));
	const represented = new Set(shown.map(machineOf));
	for (const row of ordered) {
		const key = machineResultKey(row);
		if (keys.has(key)) continue;
		const machine = machineOf(row);
		// Rows still owed to machines that have none on screen yet.
		const owed = machines.size - represented.size;
		if (represented.has(machine) && picked.length + owed >= budget) continue;
		picked.push(row);
		keys.add(key);
		represented.add(machine);
	}
	return picked;
}

/**
 * Whether one query only refines the other's words ("orb" and "orbit", either
 * way, or "orbit" and "orbit status"): rows found for one are plausible for the
 * other while it loads. Rows for an unrelated earlier query are not.
 *
 * Only plain words refine. Editing an operator or its value changes what the
 * query means rather than narrowing it: `project:alpha` to `project:alpha2`
 * is another project, `!test` to `!testing` excludes something else, and an
 * added `harness:codex` filters out rows the shorter query found.
 */
export function isQueryRefinement(previous: string, next: string): boolean {
	const before = previous.trim().toLowerCase();
	const after = next.trim().toLowerCase();
	if (!before || !after) return false;
	if (before === after) return true;
	const [shorter, longer] = before.length <= after.length ? [before, after] : [after, before];
	if (!longer.startsWith(shorter)) return false;
	// Every word the edit touched: the last word, when the edit continues it
	// rather than starting a new one after a space, and any words it added.
	let from = shorter.length;
	if (!/\s/.test(longer.charAt(from))) while (from > 0 && !/\s/.test(longer.charAt(from - 1))) from--;
	return longer.slice(from).split(/\s+/).filter(Boolean).every(isPlainSearchWord);
}

/** A word with no operator syntax: no `key:`, quote, or leading `!`, `^` or `'`. */
function isPlainSearchWord(word: string): boolean {
	return !/[:"]/.test(word) && !/^[!^']/.test(word);
}

/**
 * A remote machine's result, bounded before the palette shows it: matched
 * spans that do not fit the snippet are dropped, an oversized snippet is cut
 * around its first match, and an oversized name is cut. Another machine's AO
 * is the person's own, but it may be older, newer or broken.
 */
export function boundMachineResult(result: SessionSearchResult): SessionSearchResult {
	const snippet = typeof result.snippet === "string" ? result.snippet : "";
	let spans = validHighlights(result.highlights, snippet.length);
	let text = snippet;
	if (snippet.length > MACHINE_SNIPPET_MAX_CHARS) {
		const first = spans[0]?.start ?? 0;
		const start = Math.max(0, Math.min(first - MACHINE_SNIPPET_LEAD_CHARS, snippet.length - MACHINE_SNIPPET_MAX_CHARS));
		const end = start + MACHINE_SNIPPET_MAX_CHARS;
		const lead = start > 0 ? "…" : "";
		text = `${lead}${snippet.slice(start, end)}${end < snippet.length ? "…" : ""}`;
		spans = spans
			.filter((span) => span.start >= start && span.end <= end)
			.map((span) => ({ start: span.start - start + lead.length, end: span.end - start + lead.length }));
	}
	const name = result.sessionName && result.sessionName.length > MACHINE_SESSION_NAME_MAX_CHARS
		? `${result.sessionName.slice(0, MACHINE_SESSION_NAME_MAX_CHARS)}…`
		: result.sessionName;
	const bounded: SessionSearchResult = { ...result, snippet: text };
	if (spans.length > 0) bounded.highlights = spans;
	else delete bounded.highlights;
	if (name !== undefined) bounded.sessionName = name;
	return bounded;
}

/** In-range, non-empty, non-overlapping spans, in order. */
function validHighlights(highlights: readonly SessionSearchHighlight[] | undefined, length: number): SessionSearchHighlight[] {
	const spans = (highlights ?? [])
		.filter((span) => Number.isInteger(span.start) && Number.isInteger(span.end) && span.start >= 0 && span.end <= length && span.start < span.end)
		.sort((left, right) => left.start - right.start);
	const kept: SessionSearchHighlight[] = [];
	for (const span of spans) {
		if (kept.length > 0 && span.start < kept[kept.length - 1]!.end) continue;
		kept.push({ start: span.start, end: span.end });
	}
	return kept;
}

const SESSION_SEARCH_UNAVAILABLE = "SESSION_SEARCH_UNAVAILABLE";

/**
 * Names why a remote machine did not answer.
 *
 * A tunnel or proxy in front of a machine answers 502, 504, 520-527 or 530 when
 * the machine behind it is gone (a quick tunnel whose machine stopped answers
 * 530), and a request that cannot connect at all throws: both are "offline".
 * A daemon whose session search is off answers 503 with its own code.
 */
export function classifyMachineSearchFailure(status: number | undefined, error?: unknown, code?: string): MachineSearchOutcome {
	if (status === undefined) {
		if (error instanceof DOMException && (error.name === "TimeoutError" || error.name === "AbortError")) return "timed_out";
		if (error instanceof TypeError) return "offline";
		return "failed";
	}
	if (code === SESSION_SEARCH_UNAVAILABLE) return "search_off";
	if (status === 401 || status === 403) return "unauthorized";
	if (status === 404 || status === 405 || status === 501) return "needs_update";
	if (status === 502 || status === 504 || (status >= 520 && status <= 527) || status === 530) return "offline";
	return "failed";
}

/**
 * Why a saved machine that is not connected was not searched. A connection
 * that ended because the machine refused the saved password, or runs an AO this
 * desktop cannot talk to, says so rather than "offline".
 */
export function savedMachineOutcome(host: {
	status: "connecting" | "connected" | "offline";
	failureReason?: "unauthorized" | "incompatible";
}): MachineSearchOutcome {
	if (host.status === "connecting") return "connecting";
	if (host.failureReason === "unauthorized") return "unauthorized";
	if (host.failureReason === "incompatible") return "needs_update";
	return "offline";
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
		case "search_off":
			return t("search.machineSearchOff");
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
