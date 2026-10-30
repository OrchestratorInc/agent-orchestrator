import type { AndroidSymbol } from "expo-symbols";
import type { IOSSymbol } from "../symbols";

// Keys a phone keyboard lacks — sent straight to the PTY as escape sequences.
//
// These cannot go through the REST `/sessions/{id}/send` route: the daemon runs
// SanitizeControlChars on that payload (backend/internal/domain/text.go), which
// strips every one of them. The mux is the only path for control bytes.
//
// Exactly eight, and the row divides its width between them — see KeyRow.
export type ControlKey = {
	/** Stable identity, for React keys. */
	id: string;
	seq: string;
	/** What screen readers call the key. */
	hint: string;
	/**
	 * The key cap: each system's own symbol for the key. Material Symbols has no
	 * Escape key, so that cap has no Android symbol and shows `androidText`.
	 */
	symbol: { ios: IOSSymbol; android?: AndroidSymbol };
	/** Text after the symbol, for a chord: ⌃C. */
	suffix?: string;
	/** Android's cap where it has no symbol. */
	androidText?: string;
	/** Held down, the key repeats, as an arrow key does on a hardware keyboard. */
	repeats?: boolean;
};

export const CONTROL_KEYS: ControlKey[] = [
	{ id: "esc", seq: "\x1b", hint: "Escape", symbol: { ios: "escape" }, androidText: "esc" },
	{ id: "tab", seq: "\t", hint: "Tab", symbol: { ios: "arrow.right.to.line", android: "keyboard_tab" } },
	{ id: "interrupt", seq: "\x03", hint: "Interrupt", symbol: { ios: "control", android: "keyboard_control_key" }, suffix: "C" },
	{ id: "left", seq: "\x1b[D", hint: "Left", symbol: { ios: "arrow.left", android: "keyboard_arrow_left" }, repeats: true },
	{ id: "up", seq: "\x1b[A", hint: "Up", symbol: { ios: "arrow.up", android: "keyboard_arrow_up" }, repeats: true },
	{ id: "down", seq: "\x1b[B", hint: "Down", symbol: { ios: "arrow.down", android: "keyboard_arrow_down" }, repeats: true },
	{ id: "right", seq: "\x1b[C", hint: "Right", symbol: { ios: "arrow.right", android: "keyboard_arrow_right" }, repeats: true },
	{ id: "enter", seq: "\r", hint: "Enter", symbol: { ios: "return", android: "keyboard_return" } },
];

/** How long an arrow must be held before it repeats, and how fast it then repeats. */
export const KEY_REPEAT = { delayMs: 400, intervalMs: 80 } as const;
