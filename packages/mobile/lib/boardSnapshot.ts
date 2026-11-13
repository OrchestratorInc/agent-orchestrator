import AsyncStorage from "@react-native-async-storage/async-storage";
import type { ArchiveSummary, DashboardSession, DashboardStats, OrchestratorLink, ProjectInfo } from "./api";

// The selected machine's last board, saved on the phone. A phone that closes
// the app in the background (cheaper ones often do) used to bring it back to
// an empty board and a loader until it had reconnected and fetched the list
// again, which on a slow network takes a while. Now the last board shows at
// once, under the stale banner's "showing data from …", and the first poll
// replaces it.

const KEY_PREFIX = "ao.board.v1.";
/** A board older than this is not shown: after a week it misleads more than it helps. */
export const BOARD_KEEP_MS = 7 * 24 * 60 * 60 * 1000;
/** Above this, a board is not saved: Android keeps one value under ~2 MB. */
export const BOARD_MAX_CHARS = 1_500_000;

export type SavedBoard = {
	v: 1;
	machine: string;
	savedAt: number;
	sessions: DashboardSession[];
	orchestrators: OrchestratorLink[];
	projects: ProjectInfo[] | null;
	archive: ArchiveSummary | null;
	stats: DashboardStats;
	notificationsUnread: number;
};

export function boardKey(machine: string): string {
	return `${KEY_PREFIX}${machine}`;
}

/** The serialized board, or null when it is too large to keep. */
export function serializeBoard(board: Omit<SavedBoard, "v">): string | null {
	const text = JSON.stringify({ v: 1, ...board });
	return text.length > BOARD_MAX_CHARS ? null : text;
}

/** A saved board read back, or null when it is missing, another machine's, too old or unreadable. */
export function parseBoard(raw: string | null, machine: string, now = Date.now()): SavedBoard | null {
	if (!raw) return null;
	try {
		const board = JSON.parse(raw) as Partial<SavedBoard>;
		if (board.v !== 1 || board.machine !== machine || typeof board.savedAt !== "number") return null;
		if (now - board.savedAt >= BOARD_KEEP_MS || !Array.isArray(board.sessions) || !Array.isArray(board.orchestrators)) return null;
		return {
			v: 1,
			machine,
			savedAt: board.savedAt,
			sessions: board.sessions,
			orchestrators: board.orchestrators,
			projects: Array.isArray(board.projects) ? board.projects : null,
			archive: board.archive ?? null,
			stats: board.stats ?? {},
			notificationsUnread: typeof board.notificationsUnread === "number" ? board.notificationsUnread : 0,
		};
	} catch {
		return null;
	}
}

export async function loadBoard(machine: string): Promise<SavedBoard | null> {
	try {
		return parseBoard(await AsyncStorage.getItem(boardKey(machine)), machine);
	} catch {
		return null;
	}
}

/** Saves the newest board at most every `everyMs`, and at once when asked (leaving the app). */
export function createBoardSaver(write: (key: string, text: string) => Promise<void> = (key, text) => AsyncStorage.setItem(key, text), everyMs = 15_000) {
	let pending: Omit<SavedBoard, "v"> | null = null;
	let lastWrite = 0;
	let timer: ReturnType<typeof setTimeout> | undefined;
	let chain = Promise.resolve();
	const flush = () => {
		clearTimeout(timer);
		timer = undefined;
		if (!pending) return chain;
		const board = pending;
		pending = null;
		lastWrite = Date.now();
		const text = serializeBoard(board);
		if (text) chain = chain.then(() => write(boardKey(board.machine), text)).catch(() => {});
		return chain;
	};
	return {
		schedule(board: Omit<SavedBoard, "v">) {
			pending = board;
			if (timer) return;
			timer = setTimeout(() => void flush(), Math.max(0, lastWrite + everyMs - Date.now()));
		},
		flush,
	};
}
