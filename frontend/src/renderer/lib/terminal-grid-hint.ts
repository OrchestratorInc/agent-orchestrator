// The grid the session terminal pane last showed. A new shell tab opens in that
// same pane, so its create request carries this grid and the daemon starts the
// shell at once, in parallel with the tab mounting xterm, instead of waiting
// for the tab to attach and report its size (a full round trip before the
// shell even starts). With no hint yet, the shell still waits for the attach.
//
// Saved across launches so the first terminals after a restart, which open
// before any pane has measured, start early too. A stale grid is harmless: the
// tab resizes the PTY to its real grid as soon as it attaches.

type TerminalGrid = { cols: number; rows: number };

const STORAGE_KEY = "ao.terminalPaneGrid";

function isGrid(cols: unknown, rows: unknown): boolean {
	return (
		Number.isInteger(cols) &&
		Number.isInteger(rows) &&
		(cols as number) >= 2 &&
		(cols as number) <= 1000 &&
		(rows as number) >= 1 &&
		(rows as number) <= 1000
	);
}

function loadSavedGrid(): TerminalGrid | null {
	try {
		const raw = globalThis.localStorage?.getItem(STORAGE_KEY);
		if (!raw) return null;
		const parsed = JSON.parse(raw) as Partial<TerminalGrid>;
		return isGrid(parsed.cols, parsed.rows) ? { cols: parsed.cols!, rows: parsed.rows! } : null;
	} catch {
		return null;
	}
}

let lastPaneGrid: TerminalGrid | null | undefined;

export function rememberPaneTerminalGrid(cols: number, rows: number) {
	if (!isGrid(cols, rows)) return;
	if (lastPaneGrid?.cols === cols && lastPaneGrid.rows === rows) return;
	lastPaneGrid = { cols, rows };
	try {
		globalThis.localStorage?.setItem(STORAGE_KEY, JSON.stringify(lastPaneGrid));
	} catch {
		// Storage unavailable: the hint still lasts for this launch.
	}
}

export function paneTerminalGridHint(): TerminalGrid | null {
	if (lastPaneGrid === undefined) lastPaneGrid = loadSavedGrid();
	return lastPaneGrid;
}
