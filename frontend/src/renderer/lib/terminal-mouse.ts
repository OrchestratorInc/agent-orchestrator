export type TerminalClickMode = "app" | "select";

type ModifierEvent = Pick<MouseEvent, "altKey" | "shiftKey">;

export function forcesTerminalSelection(event: ModifierEvent, mac: boolean): boolean {
	return mac ? event.altKey : event.shiftKey;
}

type ForceSelectionEvent = ModifierEvent & Pick<MouseEvent, "button">;

export function shouldForceTerminalSelection(
	event: ForceSelectionEvent,
	options: { mode: TerminalClickMode; mac: boolean; overLink: boolean },
): boolean {
	if (options.mode === "select") return true;
	if (event.button !== 0) return true;
	if (options.overLink) return true;
	return forcesTerminalSelection(event, options.mac);
}

const SGR_MOUSE_REPORT = /^\x1b\[<(\d{1,4});\d{1,5};\d{1,5}[Mm]$/;
const X10_MOUSE_REPORT = /^\x1b\[M([\x20-\xff])[\x20-\xff]{2}$/;
const WHEEL_BUTTON_BIT = 64;

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

export function isMouseHoverReport(data: string): boolean {
	const sgr = SGR_MOUSE_REPORT.exec(data);
	const code = sgr ? Number(sgr[1]) : X10_MOUSE_REPORT.test(data) ? data.charCodeAt(3) - 32 : null;
	if (code === null) return false;
	return (code & MOTION_BIT) !== 0 && (code & 3) === NO_BUTTON;
}

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
