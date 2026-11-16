// Mouse routing for the embedded xterm terminal.
//
// A program that enables mouse tracking (DECSET 1000/1002/1003) expects the
// terminal to report clicks and drags to it, the way iTerm2, Ghostty, VS Code
// and the other xterm.js hosts do. A modifier key still forces a local text
// selection: Option on macOS (xterm's macOptionClickForcesSelection) and Shift
// elsewhere (xterm's built-in rule).

/** "app": report clicks to a program that asked for the mouse. "select": always select text. */
export type TerminalClickMode = "app" | "select";

type ModifierEvent = Pick<MouseEvent, "altKey" | "shiftKey">;

/** The modifier that forces a local text selection over a mouse-tracking program. */
export function forcesTerminalSelection(event: ModifierEvent, mac: boolean): boolean {
	return mac ? event.altKey : event.shiftKey;
}

type ForceSelectionEvent = ModifierEvent & Pick<MouseEvent, "button">;

/**
 * xterm consults this before reporting a press to the program. Returning true
 * keeps the press local (a text selection, or a link activation) and away
 * from the program.
 *
 * Only the primary button is ever reported: the right button opens AO's
 * context menu, and under tmux a reported right press would open tmux's own
 * menu instead. The middle button keeps today's behavior. A press on a link
 * stays local so xterm's linkifier opens it and the program never sees a click
 * it might also act on.
 */
export function shouldForceTerminalSelection(
	event: ForceSelectionEvent,
	options: { mode: TerminalClickMode; mac: boolean; overLink: boolean },
): boolean {
	if (options.mode === "select") return true;
	if (event.button !== 0) return true;
	if (options.overLink) return true;
	return forcesTerminalSelection(event, options.mac);
}

// xterm 6.1 encodes reports as SGR (`CSI < b ; x ; y M|m`, also used by
// SGR-pixels) on onData, or as the legacy X10 form (`CSI M` + three bytes) on
// onBinary. Each report arrives as its own event.
const SGR_MOUSE_REPORT = /^\x1b\[<(\d{1,4});\d{1,5};\d{1,5}[Mm]$/;
const X10_MOUSE_REPORT = /^\x1b\[M([\x20-\xff])[\x20-\xff]{2}$/;
const WHEEL_BUTTON_BIT = 64;

/**
 * Whether `data`, emitted by xterm, is one complete mouse report AO should
 * forward to the PTY. Wheel reports are rejected because AO synthesizes wheel
 * input itself (see the wheel handler in XtermTerminal). X10 reports past
 * column/row 95 contain bytes above 0x7f, which AO's text input path cannot
 * carry intact, so those are dropped; tmux and current agent TUIs use SGR.
 */
export function isForwardableMouseReport(data: string): boolean {
	const sgr = SGR_MOUSE_REPORT.exec(data);
	if (sgr) return (Number(sgr[1]) & WHEEL_BUTTON_BIT) === 0;
	const x10 = X10_MOUSE_REPORT.exec(data);
	if (!x10) return false;
	if (/[\x80-\xff]/.test(data)) return false;
	return ((x10[1].charCodeAt(0) - 32) & WHEEL_BUTTON_BIT) === 0;
}

const MOTION_BIT = 32;
const NO_BUTTON = 3;

/**
 * Whether a forwardable report is pointer movement with no button held
 * (DECSET 1003 any-event tracking). The program still gets it, but it is not
 * the user acting on the session: hovering must not count as interaction.
 */
export function isMouseHoverReport(data: string): boolean {
	const sgr = SGR_MOUSE_REPORT.exec(data);
	const code = sgr ? Number(sgr[1]) : X10_MOUSE_REPORT.test(data) ? data.charCodeAt(3) - 32 : null;
	if (code === null) return false;
	return (code & MOTION_BIT) !== 0 && (code & 3) === NO_BUTTON;
}

/**
 * Decode an OSC 52 clipboard *write* (`Pc ; base64`) to its text. Returns null
 * for a query (`?`), a clear (empty data) or malformed input: AO never answers
 * clipboard reads.
 */
export function parseOsc52Write(data: string): string | null {
	const separator = data.indexOf(";");
	if (separator === -1) return null;
	const selection = data.slice(0, separator);
	if (!/^[cpqs0-7]*$/.test(selection)) return null;
	const payload = data.slice(separator + 1);
	if (payload.length === 0 || payload === "?") return null;
	if (!/^[A-Za-z0-9+/]+={0,2}$/.test(payload)) return null;
	try {
		const binary = atob(payload);
		const bytes = Uint8Array.from(binary, (character) => character.charCodeAt(0));
		return new TextDecoder("utf-8", { fatal: true }).decode(bytes);
	} catch {
		return null;
	}
}
