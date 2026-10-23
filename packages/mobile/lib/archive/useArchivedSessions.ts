import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { getArchivedSessions, type ArchiveSummary, type DashboardSession } from "../api";
import { userFacingError } from "../connectionError";
import type { MachineAppearance } from "../machineAppearance";
import { useApp } from "../store";
import { compareArchived, mergeArchiveStreams } from "./archiveOrder";

/** One machine's archive, optionally narrowed to a project, as a board shows it. */
export type ArchiveSource = {
	hostId: string;
	/** The daemon's project filter. Also picks this source's count out of the summary. */
	projectId?: string;
	/** Set on the Workers tab, whose rows carry the machine they came from. */
	host?: { name: string; appearance?: MachineAppearance };
	summary: ArchiveSummary;
};

/** How many archived workers a source holds, according to the latest poll. */
export function archiveSourceCount(source: ArchiveSource): number {
	return source.projectId !== undefined ? source.summary.byProject[source.projectId] ?? 0 : source.summary.total;
}

/** Search reads a machine's archive in full, up to this many rows. */
export const ARCHIVE_SEARCH_LIMIT = 1000;

type Stream = {
	rows: DashboardSession[];
	cursor: string | null;
	done: boolean;
	loading: boolean;
	loaded: boolean;
	error: string | null;
	/** The count this stream was last loaded against; a different one means the archive moved. */
	seenCount: number;
};

const sourceKey = (source: ArchiveSource) => `${source.hostId}\n${source.projectId ?? ""}`;

function freshStream(source: ArchiveSource): Stream {
	const count = archiveSourceCount(source);
	return { rows: [], cursor: null, done: count === 0, loading: false, loaded: false, error: null, seenCount: count };
}

function appendPage(stream: Stream, rows: DashboardSession[], nextCursor: string | null): Stream {
	const seen = new Set(stream.rows.map((row) => row.id));
	return {
		...stream,
		rows: [...stream.rows, ...rows.filter((row) => !seen.has(row.id))],
		cursor: nextCursor,
		done: nextCursor === null,
		loading: false,
		loaded: true,
	};
}

/**
 * Page one again, after the archive changed under a loaded stream: a session
 * was archived (it is new at the top) or restored (it is gone). Rows already
 * loaded past page one stay, and so does the cursor, which is a position.
 */
function refreshFirstPage(stream: Stream, rows: DashboardSession[], nextCursor: string | null, count: number): Stream {
	if (!stream.loaded) return { ...appendPage(stream, rows, nextCursor), seenCount: count };
	const last = rows[rows.length - 1];
	const firstPageIds = new Set(rows.map((row) => row.id));
	const kept = last ? stream.rows.filter((row) => !firstPageIds.has(row.id) && compareArchived(row, last) > 0) : [];
	const morePast = !stream.done || kept.length > 0;
	return {
		...stream,
		rows: [...rows, ...kept],
		cursor: stream.done ? (kept.length > 0 ? null : nextCursor) : stream.cursor ?? nextCursor,
		done: morePast ? stream.done : nextCursor === null,
		loading: false,
		seenCount: count,
	};
}

/**
 * The archived workers behind a board, fetched from each machine a page at a
 * time while `active` (Archive open, or a search running), merged in archive
 * order. `inline` is the archive of a machine whose daemon still sends it with
 * the poll. `exhaustive` keeps loading so a search can see every row.
 */
export function useArchivedSessions(
	sources: readonly ArchiveSource[],
	options: { active: boolean; exhaustive: boolean; inline: readonly DashboardSession[]; refreshToken: number },
) {
	const { configForHost } = useApp();
	const [streams, setStreams] = useState<Record<string, Stream>>({});
	const streamsRef = useRef(streams);
	streamsRef.current = streams;
	const sourcesRef = useRef(sources);
	sourcesRef.current = sources;
	const controllers = useRef(new Map<string, AbortController>());
	const generation = useRef(0);

	// A different set of machines or projects, or a pull to refresh, starts over.
	const keys = sources.map(sourceKey).join("\t");
	useEffect(() => {
		generation.current += 1;
		for (const controller of controllers.current.values()) controller.abort();
		controllers.current.clear();
		setStreams(Object.fromEntries(sourcesRef.current.map((source) => [sourceKey(source), freshStream(source)])));
	}, [keys, options.refreshToken]);
	useEffect(() => () => {
		generation.current += 1;
		for (const controller of controllers.current.values()) controller.abort();
	}, []);

	const fetchPage = useCallback(async (key: string, mode: "next" | "refresh") => {
		const source = sourcesRef.current.find((candidate) => sourceKey(candidate) === key);
		const stream = streamsRef.current[key];
		if (!source || !stream || stream.loading || (mode === "next" && stream.done)) return;
		const config = configForHost(source.hostId);
		if (!config) {
			setStreams((current) => current[key] ? { ...current, [key]: { ...current[key], error: "This machine is offline." } } : current);
			return;
		}
		const run = generation.current;
		const controller = new AbortController();
		controllers.current.set(key, controller);
		// Marked here as well as in state so a second call in the same render cannot start a duplicate.
		streamsRef.current = { ...streamsRef.current, [key]: { ...stream, loading: true, error: null } };
		setStreams((current) => current[key] ? { ...current, [key]: { ...current[key], loading: true, error: null } } : current);
		try {
			const page = await getArchivedSessions(
				config,
				{ cursor: mode === "next" ? stream.cursor : null, projectId: source.projectId || undefined },
				{ signal: controller.signal },
			);
			if (run !== generation.current) return;
			const host = source.host;
			const rows = host
				? page.sessions.map((session) => ({ ...session, hostId: source.hostId, hostName: host.name, hostAppearance: host.appearance }))
				: page.sessions;
			const count = archiveSourceCount(source);
			setStreams((current) => {
				const previous = current[key];
				if (!previous) return current;
				return { ...current, [key]: mode === "next" ? appendPage(previous, rows, page.nextCursor) : refreshFirstPage(previous, rows, page.nextCursor, count) };
			});
		} catch (cause) {
			if (run !== generation.current || controller.signal.aborted) return;
			setStreams((current) => current[key]
				? { ...current, [key]: { ...current[key], loading: false, error: userFacingError(cause, "Couldn't load archived sessions.") } }
				: current);
		} finally {
			if (controllers.current.get(key) === controller) controllers.current.delete(key);
		}
	}, [configForHost]);

	// Opening Archive (or searching) asks each machine for its first page.
	useEffect(() => {
		if (!options.active) return;
		for (const source of sourcesRef.current) {
			const key = sourceKey(source);
			const stream = streams[key];
			if (stream && !stream.loaded && !stream.loading && !stream.done && !stream.error) void fetchPage(key, "next");
		}
	}, [options.active, streams, fetchPage]);

	// The poll's count moved: a session was archived or restored on that machine.
	const counts = sources.map((source) => `${sourceKey(source)}=${archiveSourceCount(source)}`).join("\t");
	useEffect(() => {
		for (const source of sourcesRef.current) {
			const key = sourceKey(source);
			const stream = streamsRef.current[key];
			const count = archiveSourceCount(source);
			if (!stream || stream.seenCount === count) continue;
			if (stream.loaded && options.active) void fetchPage(key, "refresh");
			else setStreams((current) => current[key] ? { ...current, [key]: { ...freshStream(source) } } : current);
		}
	}, [counts, options.active, fetchPage]);

	// A search reads on until it has seen everything (within the cap).
	useEffect(() => {
		if (!options.active || !options.exhaustive) return;
		for (const [key, stream] of Object.entries(streams)) {
			if (stream.loaded && !stream.done && !stream.loading && !stream.error && stream.rows.length < ARCHIVE_SEARCH_LIMIT) void fetchPage(key, "next");
		}
	}, [options.active, options.exhaustive, streams, fetchPage]);

	const inline = useMemo(() => [...options.inline].sort(compareArchived), [options.inline]);
	const ordered = useMemo(() => sources.map((source) => streams[sourceKey(source)]), [sources, streams]);
	const merged = useMemo(() => mergeArchiveStreams([
		{ rows: inline, done: true },
		// A machine that failed, or a search that hit its cap, holds nothing back:
		// its rows show as they are rather than hiding every other machine's.
		...ordered.map((stream) => stream
			? { rows: stream.rows, done: stream.done || stream.error !== null || stream.rows.length >= ARCHIVE_SEARCH_LIMIT }
			: { rows: [], done: true }),
	]), [inline, ordered]);
	const limitingRef = useRef<number | null>(null);
	limitingRef.current = merged.limiting;

	const loadMore = useCallback(() => {
		const limiting = limitingRef.current;
		if (limiting === null || limiting === 0) return;
		const source = sourcesRef.current[limiting - 1];
		if (source) void fetchPage(sourceKey(source), "next");
	}, [fetchPage]);
	const retry = useCallback(() => {
		for (const [key, stream] of Object.entries(streamsRef.current)) {
			if (!stream.error) continue;
			streamsRef.current = { ...streamsRef.current, [key]: { ...stream, error: null } };
			setStreams((current) => current[key] ? { ...current, [key]: { ...current[key], error: null } } : current);
			void fetchPage(key, "next");
		}
	}, [fetchPage]);

	const total = inline.length + sources.reduce((sum, source) => sum + archiveSourceCount(source), 0);
	return {
		rows: merged.rows,
		total,
		loading: ordered.some((stream) => stream?.loading),
		error: ordered.find((stream) => stream?.error)?.error ?? null,
		hasMore: ordered.some((stream) => stream && !stream.done && !stream.error),
		loadMore,
		retry,
	};
}
