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
	}
}

export function paneTerminalGridHint(): TerminalGrid | null {
	if (lastPaneGrid === undefined) lastPaneGrid = loadSavedGrid();
	return lastPaneGrid;
}
