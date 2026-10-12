import {
	isOrchestratorSession,
	newestActiveOrchestrator,
	sessionIsActive,
	type WorkspaceSession,
} from "../types/workspace";

export const GRID_MIN_WIDTH = 360;
export const GRID_MIN_HEIGHT = 280;
export const GRID_CHAT_MIN_HEIGHT = 360;
export const GRID_GAP = 6;

export type GridPreferences = {
	version: 1;
	order: string[];
	orchestratorId?: string;
	showCompleted: boolean;
	splits: Record<string, { columns: number[]; rows: number[] }>;
};

export function gridCompleted(session: WorkspaceSession): boolean {
	return (
		session.isTerminated === true ||
		session.status === "terminated" ||
		session.kanbanColumn === "archive" ||
		session.status === "merged"
	);
}

/** Never use activity/disconnection as evidence that a worker has completed. */
export function gridSessions(sessions: WorkspaceSession[], preferences: GridPreferences) {
	const orchestrators = sessions.filter(isOrchestratorSession);
	const pinned = orchestrators.find((session) => session.id === preferences.orchestratorId);
	const orchestrator =
		(pinned && sessionIsActive(pinned) ? pinned : undefined) ??
		newestActiveOrchestrator(orchestrators) ??
		pinned ??
		orchestrators[0];
	const workers = sessions.filter((session) => !isOrchestratorSession(session));
	const byId = new Map(workers.map((session) => [session.id, session]));
	const ordered = preferences.order.flatMap((id) => {
		const session = byId.get(id);
		if (!session) return [];
		byId.delete(id);
		return [session];
	});
	// Stable insertion order is based on creation, never on streamed updates.
	ordered.push(
		...[...byId.values()].sort(
			(a, b) => (a.createdAt ?? "").localeCompare(b.createdAt ?? "") || a.id.localeCompare(b.id),
		),
	);
	return {
		orchestrator,
		workers: ordered,
		visible: ordered.filter((session) => preferences.showCompleted || !gridCompleted(session)),
	};
}

/** Choose readable cells, penalizing empty cells and required scrolling. */
export function gridShape(count: number, width: number, height: number, minimumHeight = GRID_MIN_HEIGHT) {
	count = Math.max(1, count);
	width = Math.max(1, width);
	height = Math.max(1, height);
	let best = { columns: 1, rows: count, score: Infinity };
	const maxColumns = Math.min(count, Math.max(1, Math.floor((width + GRID_GAP) / (GRID_MIN_WIDTH + GRID_GAP))));
	for (let columns = 1; columns <= maxColumns; columns++) {
		const rows = Math.ceil(count / columns);
		const cellWidth =
			(Math.max(width, columns * GRID_MIN_WIDTH + (columns - 1) * GRID_GAP) - (columns - 1) * GRID_GAP) / columns;
		const cellHeight = Math.max(minimumHeight, (height - (rows - 1) * GRID_GAP) / rows);
		const scroll = Math.max(0, (rows * cellHeight + (rows - 1) * GRID_GAP) / height - 1);
		const score =
			Math.abs(Math.log(cellWidth / cellHeight / 1.35)) + ((columns * rows - count) / count) * 0.7 + scroll * 0.5;
		if (score < best.score) best = { columns, rows, score };
	}
	return { columns: best.columns, rows: best.rows };
}

/** Water filling keeps every track above its minimum after a window resize. */
export function gridTracks(count: number, available: number, minimum: number, weights?: number[]): number[] {
	available = Math.max(available, count * minimum);
	const valid = weights?.length === count && weights.every((value) => Number.isFinite(value) && value > 0);
	const source = valid ? weights! : Array.from({ length: count }, () => 1);
	const tracks = Array<number>(count).fill(0);
	const remaining = new Set(source.map((_, index) => index));
	let budget = available;
	while (remaining.size) {
		const sum = [...remaining].reduce((total, index) => total + source[index], 0);
		const small = [...remaining].filter((index) => (budget * source[index]) / sum < minimum);
		if (!small.length) {
			for (const index of remaining) tracks[index] = (budget * source[index]) / sum;
			break;
		}
		for (const index of small) {
			tracks[index] = minimum;
			budget -= minimum;
			remaining.delete(index);
		}
	}
	return tracks;
}

export function resizeGridTracks(tracks: number[], index: number, delta: number, minimum: number): number[] {
	if (index < 0 || index >= tracks.length - 1) return tracks;
	const next = [...tracks];
	const change = Math.max(minimum - tracks[index], Math.min(tracks[index + 1] - minimum, delta));
	next[index] += change;
	next[index + 1] -= change;
	return next;
}

export function gridStorageKey(projectId: string, hostId?: string) {
	return hostId ? `ao.live-grid.v1.remote.${encodeURIComponent(hostId)}.${encodeURIComponent(projectId)}`
		: `ao.live-grid.v1.${encodeURIComponent(projectId)}`;
}
export function readGridPreferences(projectId: string, hostId?: string): GridPreferences {
	const empty: GridPreferences = { version: 1, order: [], showCompleted: false, splits: {} };
	try {
		const value = JSON.parse(localStorage.getItem(gridStorageKey(projectId, hostId)) ?? "null");
		if (!value || value.version !== 1) return empty;
		const splits: GridPreferences["splits"] = {};
		for (const [key, split] of Object.entries(value.splits ?? {}) as Array<
			[string, { columns?: unknown; rows?: unknown }]
		>) {
			if (!/^\d+x\d+$/.test(key) || !split) continue;
			const valid = (track: unknown): track is number[] =>
				Array.isArray(track) &&
				track.length <= 100 &&
				track.every((v) => typeof v === "number" && Number.isFinite(v) && v > 0);
			if (valid(split.columns) && valid(split.rows)) splits[key] = { columns: split.columns, rows: split.rows };
		}
		return {
			version: 1,
			order: Array.isArray(value.order)
				? [...new Set<string>(value.order.filter((id: unknown) => typeof id === "string"))]
				: [],
			orchestratorId: typeof value.orchestratorId === "string" ? value.orchestratorId : undefined,
			showCompleted: value.showCompleted === true,
			splits,
		};
	} catch {
		return empty;
	}
}

export function writeGridPreferences(projectId: string, preferences: GridPreferences, hostId?: string) {
	try {
		localStorage.setItem(gridStorageKey(projectId, hostId), JSON.stringify(preferences));
	} catch {
		/* The current layout still works if storage is unavailable. */
	}
}
