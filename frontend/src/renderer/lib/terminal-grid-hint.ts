// The grid the session terminal pane last showed. A new shell tab opens in that
// same pane, so its create request carries this grid and the daemon starts the
// shell at once, in parallel with the tab mounting xterm, instead of waiting
// for the tab to attach and report its size (a full round trip before the
// shell even starts). With no hint yet, the shell still waits for the attach.

type TerminalGrid = { cols: number; rows: number };

let lastPaneGrid: TerminalGrid | null = null;

export function rememberPaneTerminalGrid(cols: number, rows: number) {
	if (cols >= 2 && rows >= 1) lastPaneGrid = { cols, rows };
}

export function paneTerminalGridHint(): TerminalGrid | null {
	return lastPaneGrid;
}
