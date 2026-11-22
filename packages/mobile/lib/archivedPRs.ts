import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getArchivedPRSessions, type ArchivedPRState, type DashboardSession, type PRStatePage } from "./api";
import { userFacingError } from "./connectionError";
import { hostedRowKey, type HostedSession } from "./hostedRows";
import type { MachineAppearance } from "./machineAppearance";
import { useApp } from "./store";

// The poll fetches live workers only, so a pull request merged or closed by a
// session that has since ended is in no row the PR list reads. A machine whose
// daemon pages its archive is asked for those sessions separately, all of
// them: merged ones while Merged or All is open, closed ones while All is. A
// machine that still sends its archive with the poll needs none of this: its
// ended sessions are already rows.

/** One machine's archive, optionally narrowed to a project, read for its PRs. */
export type ArchivedPRSource = {
	hostId: string;
	hostName: string;
	appearance?: MachineAppearance;
	/** The daemon's project filter. */
	projectId?: string;
	/** The archive's size from the latest poll: when it moves, the list is read again. */
	archived: number;
};

type Stream = {
	rows: HostedSession[];
	/** How many archived sessions the daemon says match, across every page. */
	total: number;
	loading: boolean;
	/** False once the daemon is known to predate the filter. */
	supported: boolean;
	error: string | null;
	/** The rows are every match from one read that reached the end, all from one list. */
	complete: boolean;
	/** The daemon's version of the list the rows came from, when complete. */
	version: string | null;
};

const EMPTY_STREAM: Stream = { rows: [], total: 0, loading: false, supported: true, error: null, complete: false, version: null };

/** Reads of a list that keeps moving under the cursor stop after this many tries. */
const MAX_READ_ATTEMPTS = 3;

/** "full" reads every page; "check" reads the first and stops when nothing moved. */
type ReadMode = "full" | "check";

/**
 * Whether a first page names the same list a complete stream holds. The
 * daemon's version covers the whole list, so a later page that changed (one
 * session restored, another ended: same total, same first page) still reads
 * on. A worker ending without a PR in this state leaves the version alone and
 * costs one page. Without a version nothing can be proven unchanged.
 */
export function firstPageUnchanged(held: Pick<Stream, "complete" | "supported" | "version">, first: PRStatePage): boolean {
	return held.complete && held.supported && first.version !== null && first.version === held.version;
}

/**
 * Whether one read saw one list end to end: every page from the same version,
 * and as many sessions as that list holds. Pages fetched while the list moved
 * under the cursor fail this, and the read starts over.
 */
export function readIsConsistent(rows: number, total: number, versions: ReadonlySet<string | null>): boolean {
	return versions.size === 1 && rows === total;
}

const sourceKey = (source: Pick<ArchivedPRSource, "hostId" | "projectId">) => `${source.hostId}\n${source.projectId ?? ""}`;
const streamKey = (source: Pick<ArchivedPRSource, "hostId" | "projectId">, state: ArchivedPRState) => `${sourceKey(source)}\n${state}`;

/**
 * Reads a cursor to its end, handing each page's new sessions to `onPage`.
 * "unsupported" when the daemon predates the filter. A session can move
 * between pages when its PR changes state again; it is kept once.
 */
export async function readAllPRStatePages(
	fetchPage: (cursor: string | null) => Promise<PRStatePage | null>,
	onPage: (sessions: DashboardSession[], page: PRStatePage) => void,
	signal?: AbortSignal,
): Promise<"done" | "unsupported" | "aborted"> {
	const seen = new Set<string>();
	let cursor: string | null = null;
	const cursors = new Set<string>();
	for (;;) {
		if (signal?.aborted) return "aborted";
		const page = await fetchPage(cursor);
		if (signal?.aborted) return "aborted";
		if (!page) return "unsupported";
		const fresh = page.sessions.filter((session) => !seen.has(session.id));
		for (const session of fresh) seen.add(session.id);
		onPage(fresh, page);
		// A cursor seen before would loop forever; the list has been read.
		if (!page.nextCursor || cursors.has(page.nextCursor)) return "done";
		cursors.add(page.nextCursor);
		cursor = page.nextCursor;
	}
}

/**
 * The live rows, then the archived ones no live row already stands for: a
 * session archived between the poll and the archive read must not list twice.
 */
export function withArchivedPRSessions(live: readonly HostedSession[], archived: readonly HostedSession[]): HostedSession[] {
	if (archived.length === 0) return live as HostedSession[];
	const liveKeys = new Set(live.map((session) => hostedRowKey(session.hostId, session.id)));
	return [...live, ...archived.filter((session) => !liveKeys.has(hostedRowKey(session.hostId, session.id)))];
}

/**
 * How many archived sessions with a PR in a state are counted but not loaded
 * yet. Each holds at least one such PR, so adding this to the loaded count
 * keeps a filter's number from climbing page by page while the list loads.
 */
export function unloadedArchivedSessions(streams: Iterable<Pick<Stream, "rows" | "total" | "supported">>): number {
	let count = 0;
	for (const stream of streams) {
		if (stream.supported) count += Math.max(0, stream.total - stream.rows.length);
	}
	return count;
}

/**
 * An archived row as the PR list reads it: tagged with its machine and holding
 * only its PRs in the state it was read for. A PR the ended session left open
 * would otherwise join the Open filter only after Merged or All had been
 * opened.
 */
export function archivedPRRow(source: Pick<ArchivedPRSource, "hostId" | "hostName" | "appearance">, session: DashboardSession, state: ArchivedPRState): HostedSession {
	const prs = (session.prs?.length ? session.prs : session.pr ? [session.pr] : []).filter((pr) => pr.state === state);
	return { ...session, pr: prs[0] ?? null, prs, hostId: source.hostId, hostName: source.hostName, hostAppearance: source.appearance };
}

/**
 * One row per archived session: a session read for both its merged and its
 * closed PRs keeps the first row and gains the other's PRs.
 */
export function mergeArchivedRows(lists: readonly (readonly HostedSession[])[]): HostedSession[] {
	const byKey = new Map<string, HostedSession>();
	for (const rows of lists) {
		for (const row of rows) {
			const key = hostedRowKey(row.hostId, row.id);
			const existing = byKey.get(key);
			if (!existing) byKey.set(key, row);
			else byKey.set(key, { ...existing, prs: [...(existing.prs ?? []), ...(row.prs ?? [])] });
		}
	}
	return [...byKey.values()];
}

/**
 * Every archived worker with a PR in one of `states` on each source, read to
 * the end. A stream is read again when its archive's size moves or
 * `refreshToken` changes; a first read shows its pages as they land, a re-read
 * keeps the rows on screen until it has them all. A state no longer asked for
 * keeps what it read, so going back to its filter shows it at once.
 */
export function useArchivedPRs(sources: readonly ArchivedPRSource[], options: { states: readonly ArchivedPRState[]; refreshToken: number }) {
	const { configForHost } = useApp();
	const [streams, setStreams] = useState<Record<string, Stream>>({});
	const streamsRef = useRef(streams);
	streamsRef.current = streams;
	const sourcesRef = useRef(sources);
	sourcesRef.current = sources;
	const runs = useRef(new Map<string, { controller: AbortController; archived: number; refreshToken: number; inFlight: boolean; offline: boolean }>());
	// Bumped when a read settles, so a count that moved while it ran is checked then.
	const [settled, setSettled] = useState(0);

	const update = useCallback((key: string, change: (stream: Stream) => Stream) => {
		setStreams((current) => ({ ...current, [key]: change(current[key] ?? EMPTY_STREAM) }));
	}, []);

	const read = useCallback(async (source: ArchivedPRSource, state: ArchivedPRState, key: string, controller: AbortController, mode: ReadMode) => {
		const config = configForHost(source.hostId);
		if (!config) return;
		const fetchPage = (cursor: string | null) =>
			getArchivedPRSessions(config, { prState: state, cursor, projectId: source.projectId }, { signal: controller.signal });
		const held = streamsRef.current[key] ?? EMPTY_STREAM;
		const firstRead = held.rows.length === 0;
		try {
			// A moved archive count is checked against the first page before
			// anything is shown as loading; most ended workers change nothing here.
			let first: PRStatePage | null | undefined;
			if (mode === "check" && held.complete) {
				first = await fetchPage(null);
				if (controller.signal.aborted) return;
				if (first && firstPageUnchanged(held, first)) return;
			}
			update(key, (stream) => ({ ...stream, loading: true, error: null }));
			for (let attempt = 1; ; attempt += 1) {
				const rows: HostedSession[] = [];
				let total = 0;
				const versions = new Set<string | null>();
				// Only a first read shows pages as they land; a retry would shrink them.
				const progressive = firstRead && attempt === 1;
				const reuse = attempt === 1 ? first : undefined;
				const outcome = await readAllPRStatePages(
					(cursor) => cursor === null && reuse !== undefined ? Promise.resolve(reuse) : fetchPage(cursor),
					(sessions, page) => {
						rows.push(...sessions.map((session) => archivedPRRow(source, session, state)));
						total = page.total;
						versions.add(page.version);
						if (progressive) {
							const shown = [...rows];
							update(key, (stream) => ({ ...stream, rows: shown, total: page.total }));
						}
					},
					controller.signal,
				);
				if (outcome === "aborted") return;
				if (outcome === "unsupported") {
					update(key, () => ({ ...EMPTY_STREAM, supported: false }));
					return;
				}
				if (readIsConsistent(rows.length, total, versions)) {
					const [version] = versions;
					update(key, () => ({ rows, total, loading: false, supported: true, error: null, complete: true, version: version ?? null }));
					return;
				}
				if (attempt >= MAX_READ_ATTEMPTS) {
					// The list kept moving under the cursor: show what was read, count
					// only that, and let the next change or refresh read it again.
					update(key, () => ({ rows, total: rows.length, loading: false, supported: true, error: null, complete: false, version: null }));
					return;
				}
			}
		} catch (cause) {
			if (controller.signal.aborted) return;
			// Whatever was on screen stays; a pull to refresh reads again. The
			// total shrinks to it, so the counts claim no PR the list cannot show,
			// and the stream is no longer complete, so the next check reads it all.
			update(key, (stream) => ({
				...stream, total: stream.rows.length, loading: false, complete: false, version: null,
				error: userFacingError(cause, "Couldn't load past pull requests."),
			}));
		}
	}, [configForHost, update]);

	// Whether each machine can be reached is part of the plan: its config appears
	// without the lookup itself changing, and a source skipped while its machine
	// was offline must be read once it answers.
	const plan = sources.map((source) => `${sourceKey(source)}=${source.archived}=${configForHost(source.hostId) ? "on" : "off"}`).join("\t");
	const wanted = options.states.join(",");
	useEffect(() => {
		const current = new Set(sourcesRef.current.map(sourceKey));
		const stillShown = (key: string) => current.has(key.slice(0, key.lastIndexOf("\n")));
		for (const [key, run] of runs.current) {
			if (stillShown(key)) continue;
			run.controller.abort();
			runs.current.delete(key);
		}
		setStreams((existing) => {
			const kept = Object.entries(existing).filter(([key]) => stillShown(key));
			return kept.length === Object.keys(existing).length ? existing : Object.fromEntries(kept);
		});
		const start = (source: ArchivedPRSource, state: ArchivedPRState, key: string, mode: ReadMode) => {
			const controller = new AbortController();
			const run = { controller, archived: source.archived, refreshToken: options.refreshToken, inFlight: true, offline: false };
			runs.current.set(key, run);
			void read(source, state, key, controller, mode).finally(() => {
				if (runs.current.get(key) !== run) return;
				run.inFlight = false;
				setSettled((n) => n + 1);
			});
		};
		// Note every machine that went away, for every stream read so far, whether
		// or not its filter is open: a failed read retries once the machine is back
		// and its filter is shown again.
		for (const [key, run] of runs.current) {
			if (!configForHost(key.slice(0, key.indexOf("\n")))) run.offline = true;
		}
		for (const state of options.states) {
			for (const source of sourcesRef.current) {
				const key = streamKey(source, state);
				const previous = runs.current.get(key);
				// An offline machine is not marked read, so the next change or refresh
				// tries it again.
				if (!configForHost(source.hostId)) continue;
				// Back online: the return stays pending while a read is still in
				// flight (its failure may land after the machine answers again). Once
				// it settles, a failed read is retried once; a good one just clears it.
				if (previous?.offline && !previous.inFlight) {
					previous.offline = false;
					if (streamsRef.current[key]?.error) {
						start(source, state, key, "full");
						continue;
					}
				}
				// A first read or a pull to refresh reads everything again.
				if (!previous || previous.refreshToken !== options.refreshToken) {
					previous?.controller.abort();
					start(source, state, key, "full");
					continue;
				}
				// A moved archive count never cuts a read short: one in flight is
				// checked again when it settles, an idle one checks its first page.
				if (previous.archived === source.archived || previous.inFlight) continue;
				start(source, state, key, "check");
			}
		}
		// `wanted` stands for `options.states`.
	}, [plan, wanted, options.refreshToken, read, configForHost, settled]);
	useEffect(() => () => {
		for (const run of runs.current.values()) run.controller.abort();
		runs.current.clear();
	}, []);

	// Keyed on the plan, not the array: a host sync rebuilds `sources` with the
	// same machines, and the PR list must not rebuild its sections for that.
	const shown = useMemo(() => {
		const byState = (state: ArchivedPRState) => sourcesRef.current
			.map((source) => streams[streamKey(source, state)])
			.filter((stream): stream is Stream => Boolean(stream));
		return { merged: byState("merged"), closed: byState("closed") };
	}, [plan, streams]);
	const sessions = useMemo(
		() => mergeArchivedRows([...shown.merged, ...shown.closed].map((stream) => stream.rows)),
		[shown],
	);
	const all = [...shown.merged, ...shown.closed];
	return {
		sessions,
		unloaded: { merged: unloadedArchivedSessions(shown.merged), closed: unloadedArchivedSessions(shown.closed) },
		loading: { merged: shown.merged.some((stream) => stream.loading), closed: shown.closed.some((stream) => stream.loading) },
		error: all.find((stream) => stream.error)?.error ?? null,
	};
}
