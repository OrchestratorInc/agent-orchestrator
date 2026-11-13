import { useEffect, useState } from "react";
import { getArchivedSessions } from "../api";
import { useApp } from "../store";
import { latestEventAt, readPastPinned } from "./archiveOrder";
import { archiveSourceCount, type ArchiveSource } from "./useArchivedSessions";

/**
 * The archive lists pinned workers first, then the rest newest first, so the
 * newest finish is the first unpinned row or one of the pinned rows before it.
 * Pages are read until that row turns up, up to this many.
 */
const MAX_PAGES = 4;

/**
 * When the newest archived worker in scope last did anything.
 *
 * Machines that page their archive send only counts with the poll, so the time
 * takes a read per machine, usually its first page. It runs only while `enabled` (the idle board
 * is showing), and again when a machine's archive count changes.
 */
export function useLatestArchivedAt(sources: readonly ArchiveSource[], enabled: boolean): string | undefined {
	const { configForHost } = useApp();
	const [latest, setLatest] = useState<string>();
	const key = sources.map((source) => `${source.hostId}:${source.projectId ?? ""}:${archiveSourceCount(source)}`).join("\t");
	useEffect(() => {
		const withHistory = sources.filter((source) => archiveSourceCount(source) > 0);
		if (!enabled || withHistory.length === 0) {
			setLatest(undefined);
			return;
		}
		const controller = new AbortController();
		void Promise.all(withHistory.map(async (source) => {
			const config = configForHost(source.hostId);
			if (!config) return [];
			try {
				return await readPastPinned(
					(cursor) => getArchivedSessions(config, { cursor, projectId: source.projectId || undefined }, { signal: controller.signal }),
					MAX_PAGES,
				);
			} catch {
				return [];
			}
		})).then((pages) => {
			if (!controller.signal.aborted) setLatest(latestEventAt(pages.flat()));
		});
		return () => controller.abort();
		// eslint-disable-next-line react-hooks/exhaustive-deps -- `key` covers every field of `sources` read here
	}, [key, enabled, configForHost]);
	return latest;
}
