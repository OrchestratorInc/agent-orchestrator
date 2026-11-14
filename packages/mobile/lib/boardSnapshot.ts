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
/**
 * Above this many bytes, a board is not saved: Android reads one stored value
 * back only under ~2 MB, counted in UTF-8 bytes, not characters (a name in
 * another script takes two or three bytes a character).
 */
export const BOARD_MAX_BYTES = 1_500_000;

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

/** How many bytes `text` takes as UTF-8. */
export function utf8ByteLength(text: string): number {
	let bytes = 0;
	for (let index = 0; index < text.length; index++) {
		const unit = text.charCodeAt(index);
		if (unit < 0x80) bytes += 1;
		else if (unit < 0x800) bytes += 2;
		else if (unit >= 0xd800 && unit <= 0xdbff && index + 1 < text.length && (text.charCodeAt(index + 1) & 0xfc00) === 0xdc00) {
			// A surrogate pair is one character outside the BMP: four bytes.
			bytes += 4;
			index++;
		} else bytes += 3;
	}
	return bytes;
}

/** The serialized board, or null when it is too large to keep. */
export function serializeBoard(board: Omit<SavedBoard, "v">): string | null {
	const text = JSON.stringify({ v: 1, ...board });
	return utf8ByteLength(text) > BOARD_MAX_BYTES ? null : text;
}

function isRecord(value: unknown): value is Record<string, unknown> {
	return typeof value === "object" && value !== null && !Array.isArray(value);
}

/** Rows the board keys by id: each one an object with a string id. */
function isRowList(value: unknown): value is Array<{ id: string }> {
	return Array.isArray(value) && value.every((row) => isRecord(row) && typeof row.id === "string");
}

function isArchiveSummary(value: unknown): value is ArchiveSummary {
	return isRecord(value) && typeof value.total === "number" && isRecord(value.byProject)
		&& Object.values(value.byProject).every((count) => typeof count === "number");
}

/** A saved board read back, or null when it is missing, another machine's, too old or unreadable. */
export function parseBoard(raw: string | null, machine: string, now = Date.now()): SavedBoard | null {
	if (!raw) return null;
	try {
		const parsed: unknown = JSON.parse(raw);
		if (!isRecord(parsed)) return null;
		const board = parsed as Partial<SavedBoard>;
		if (board.v !== 1 || board.machine !== machine || typeof board.savedAt !== "number") return null;
		if (now - board.savedAt >= BOARD_KEEP_MS) return null;
		// Read the way a live answer is: every row keyed by its id, and an archive
		// count per project. A board that is not shaped so is dropped whole,
		// rather than handed to code that would crash on it.
		if (!isRowList(board.sessions) || !isRowList(board.orchestrators)) return null;
		if (board.projects != null && !(isRowList(board.projects) && board.projects.every((project) => typeof project.name === "string"))) return null;
		if (board.archive != null && !isArchiveSummary(board.archive)) return null;
		return {
			v: 1,
			machine,
			savedAt: board.savedAt,
			sessions: board.sessions,
			orchestrators: board.orchestrators,
			projects: board.projects ?? null,
			archive: board.archive ?? null,
			stats: isRecord(board.stats) ? board.stats : {},
			notificationsUnread: typeof board.notificationsUnread === "number" ? board.notificationsUnread : 0,
		};
	} catch {
		return null;
	}
}

/** Forgets a machine's saved board (the machine was forgotten). */
export async function forgetBoard(machine: string): Promise<void> {
	await AsyncStorage.removeItem(boardKey(machine));
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
