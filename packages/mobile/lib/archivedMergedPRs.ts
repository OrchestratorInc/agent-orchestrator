import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getArchivedPRSessions, type DashboardSession, type PRStatePage } from "./api";
import { userFacingError } from "./connectionError";
import { hostedRowKey, type HostedSession } from "./hostedRows";
import type { MachineAppearance } from "./machineAppearance";
import { useApp } from "./store";

// The poll fetches live workers only, so a pull request merged by a session
// that has since ended is in no row the PR list reads. A machine whose daemon
// pages its archive is asked for those sessions separately, all of them, while
// the Merged or All filter is open. A machine that still sends its archive with
// the poll needs none of this: its ended sessions are already rows.

/** One machine's archive, optionally narrowed to a project, read for merged PRs. */
export type MergedPRSource = {
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
};

const EMPTY_STREAM: Stream = { rows: [], total: 0, loading: false, supported: true, error: null };

export const mergedSourceKey = (source: Pick<MergedPRSource, "hostId" | "projectId">) => `${source.hostId}\n${source.projectId ?? ""}`;

/**
 * Reads a cursor to its end, handing each page's new sessions to `onPage`.
 * "unsupported" when the daemon predates the filter. A session can move
 * between pages when its PR changes state again; it is kept once.
 */
export async function readAllPRStatePages(
	fetchPage: (cursor: string | null) => Promise<PRStatePage | null>,
	onPage: (sessions: DashboardSession[], total: number) => void,
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
		onPage(fresh, page.total);
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
 * How many archived sessions with a merged PR are counted but not loaded yet.
 * Each holds at least one, so adding this to the loaded count keeps the
 * Merged filter's number from climbing page by page while the list loads.
 */
export function unloadedMergedSessions(streams: Iterable<Pick<Stream, "rows" | "total" | "supported">>): number {
	let count = 0;
	for (const stream of streams) {
		if (stream.supported) count += Math.max(0, stream.total - stream.rows.length);
	}
	return count;
}

/**
 * An archived row as the PR list reads it: tagged with its machine and holding
 * its merged PRs only. A PR the ended session left open or closed would
 * otherwise join the other filters only after Merged had been opened.
 */
export function archivedMergedRow(source: Pick<MergedPRSource, "hostId" | "hostName" | "appearance">, session: DashboardSession): HostedSession {
	const prs = (session.prs?.length ? session.prs : session.pr ? [session.pr] : []).filter((pr) => pr.state === "merged");
	return { ...session, pr: prs[0] ?? null, prs, hostId: source.hostId, hostName: source.hostName, hostAppearance: source.appearance };
}

function hosted(source: MergedPRSource, sessions: DashboardSession[]): HostedSession[] {
	return sessions.map((session) => archivedMergedRow(source, session));
}

/**
 * Every archived worker with a merged PR on each source, read to the end while
 * `active`. A source is read again when its archive's size moves or
 * `refreshToken` changes; a first read shows its pages as they land, a re-read
 * keeps the rows on screen until it has them all.
 */
export function useArchivedMergedPRs(sources: readonly MergedPRSource[], options: { active: boolean; refreshToken: number }) {
	const { configForHost } = useApp();
	const [streams, setStreams] = useState<Record<string, Stream>>({});
	const streamsRef = useRef(streams);
	streamsRef.current = streams;
	const sourcesRef = useRef(sources);
	sourcesRef.current = sources;
	const runs = useRef(new Map<string, { controller: AbortController; stamp: string }>());

	const update = useCallback((key: string, change: (stream: Stream) => Stream) => {
		setStreams((current) => ({ ...current, [key]: change(current[key] ?? EMPTY_STREAM) }));
	}, []);

	const read = useCallback(async (source: MergedPRSource, key: string, controller: AbortController) => {
		const config = configForHost(source.hostId);
		if (!config) return;
		const firstRead = (streamsRef.current[key]?.rows.length ?? 0) === 0;
		update(key, (stream) => ({ ...stream, loading: true, error: null }));
		const rows: HostedSession[] = [];
		let total = 0;
		try {
			const outcome = await readAllPRStatePages(
				(cursor) => getArchivedPRSessions(config, { prState: "merged", cursor, projectId: source.projectId }, { signal: controller.signal }),
				(sessions, pageTotal) => {
					rows.push(...hosted(source, sessions));
					total = pageTotal;
					if (firstRead) {
						const shown = [...rows];
						update(key, (stream) => ({ ...stream, rows: shown, total: pageTotal }));
					}
				},
				controller.signal,
			);
			if (outcome === "aborted") return;
			if (outcome === "unsupported") {
				update(key, () => ({ ...EMPTY_STREAM, supported: false }));
				return;
			}
			update(key, () => ({ rows, total: Math.max(total, rows.length), loading: false, supported: true, error: null }));
		} catch (cause) {
			if (controller.signal.aborted) return;
			// Whatever was on screen stays; a pull to refresh reads again.
			update(key, (stream) => ({ ...stream, loading: false, error: userFacingError(cause, "Couldn't load merged pull requests.") }));
		}
	}, [configForHost, update]);

	const plan = sources.map((source) => `${mergedSourceKey(source)}=${source.archived}`).join("\t");
	useEffect(() => {
		const current = new Set(sourcesRef.current.map(mergedSourceKey));
		for (const [key, run] of runs.current) {
			if (current.has(key)) continue;
			run.controller.abort();
			runs.current.delete(key);
		}
		setStreams((existing) => {
			const kept = Object.entries(existing).filter(([key]) => current.has(key));
			return kept.length === Object.keys(existing).length ? existing : Object.fromEntries(kept);
		});
		if (!options.active) return;
		for (const source of sourcesRef.current) {
			const key = mergedSourceKey(source);
			const stamp = `${source.archived}|${options.refreshToken}`;
			const previous = runs.current.get(key);
			// An offline machine is not marked read, so the next change or refresh tries it again.
			if (previous?.stamp === stamp || !configForHost(source.hostId)) continue;
			previous?.controller.abort();
			const controller = new AbortController();
			runs.current.set(key, { controller, stamp });
			void read(source, key, controller);
		}
	}, [plan, options.active, options.refreshToken, read, configForHost]);
	useEffect(() => () => {
		for (const run of runs.current.values()) run.controller.abort();
		runs.current.clear();
	}, []);

	// Keyed on the plan, not the array: a host sync rebuilds `sources` with the
	// same machines, and the PR list must not rebuild its sections for that.
	const shown = useMemo(
		() => sourcesRef.current.map((source) => streams[mergedSourceKey(source)]).filter((stream): stream is Stream => Boolean(stream)),
		[plan, streams],
	);
	const sessions = useMemo(() => shown.flatMap((stream) => stream.rows), [shown]);
	return {
		sessions,
		unloaded: unloadedMergedSessions(shown),
		loading: shown.some((stream) => stream.loading),
		error: shown.find((stream) => stream.error)?.error ?? null,
	};
}
