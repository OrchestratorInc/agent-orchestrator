import type { DashboardSession } from "../api";

/**
 * A timestamp as [seconds since the epoch, nanoseconds], the precision the
 * daemon orders by. `Date.parse` alone keeps milliseconds, so rows inside one
 * millisecond fell to the id and disagreed with the daemon's page order.
 * Every daemon sends updatedAt; the fallbacks only order rows built without it.
 */
function instantOf(session: DashboardSession): [number, number] {
	const raw = session.updatedAt || session.lastEventAt || session.lastActivityAt || "";
	const parts = /^(.+?)(?:\.(\d+))?(Z|[+-]\d{2}:\d{2})$/i.exec(raw);
	if (!parts) {
		const ms = Date.parse(raw);
		return Number.isNaN(ms) ? [0, 0] : [Math.floor(ms / 1000), (ms % 1000) * 1_000_000];
	}
	const ms = Date.parse(parts[1] + parts[3]);
	if (Number.isNaN(ms)) return [0, 0];
	return [Math.floor(ms / 1000), Number((parts[2] ?? "").slice(0, 9).padEnd(9, "0"))];
}

/**
 * The archive's order: pinned first, then the most recently updated, then by
 * id. The same order the daemon pages in (and desktop's archive strip), so a
 * row never moves when the next page arrives.
 */
export function compareArchived(a: DashboardSession, b: DashboardSession): number {
	if (Boolean(a.isPinned) !== Boolean(b.isPinned)) return a.isPinned ? -1 : 1;
	const [aSeconds, aNanos] = instantOf(a);
	const [bSeconds, bNanos] = instantOf(b);
	if (aSeconds !== bSeconds) return bSeconds - aSeconds;
	if (aNanos !== bNanos) return bNanos - aNanos;
	return a.id < b.id ? 1 : a.id > b.id ? -1 : 0;
}

/** One machine's archive as loaded so far. `done` once its last page is in. */
export type ArchiveStream = { rows: readonly DashboardSession[]; done: boolean };

/**
 * Several machines' archives as one list.
 *
 * Each machine pages its own archive, so a row can only be placed once every
 * machine still loading has reached it: until then another machine's next
 * page could hold rows that belong above it. Rows past that point wait, which
 * keeps the list from growing in the middle while someone scrolls it.
 * `limiting` is the stream whose next page lets the list grow, if any.
 */
export function mergeArchiveStreams(streams: readonly ArchiveStream[]): { rows: DashboardSession[]; limiting: number | null } {
	let limiting: number | null = null;
	let boundary: DashboardSession | undefined;
	for (const [index, stream] of streams.entries()) {
		if (stream.done) continue;
		const last = stream.rows[stream.rows.length - 1];
		if (!last) return { rows: [], limiting: index };
		if (!boundary || compareArchived(last, boundary) < 0) {
			boundary = last;
			limiting = index;
		}
	}
	const rows = streams.flatMap((stream) => stream.rows).sort(compareArchived);
	return { rows: boundary ? rows.filter((row) => compareArchived(row, boundary) <= 0) : rows, limiting };
}
