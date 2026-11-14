// Key presses for trusted input (CDP Input.dispatchKeyEvent). Accepts the same
// names and chords as `ao browser press`: "Enter", "ArrowDown", "Control+A".

import { BridgeError } from "./protocol";

export const MODIFIER_BITS = { Alt: 1, Control: 2, Meta: 4, Shift: 8 } as const;
type Modifier = keyof typeof MODIFIER_BITS;

type KeyDefinition = { key: string; code: string; keyCode: number; text?: string };

const NAMED: Record<string, KeyDefinition> = {
	Enter: { key: "Enter", code: "Enter", keyCode: 13, text: "\r" },
	Tab: { key: "Tab", code: "Tab", keyCode: 9 },
	Escape: { key: "Escape", code: "Escape", keyCode: 27 },
	Backspace: { key: "Backspace", code: "Backspace", keyCode: 8 },
	Delete: { key: "Delete", code: "Delete", keyCode: 46 },
	Space: { key: " ", code: "Space", keyCode: 32, text: " " },
	ArrowUp: { key: "ArrowUp", code: "ArrowUp", keyCode: 38 },
	ArrowDown: { key: "ArrowDown", code: "ArrowDown", keyCode: 40 },
	ArrowLeft: { key: "ArrowLeft", code: "ArrowLeft", keyCode: 37 },
	ArrowRight: { key: "ArrowRight", code: "ArrowRight", keyCode: 39 },
	Home: { key: "Home", code: "Home", keyCode: 36 },
	End: { key: "End", code: "End", keyCode: 35 },
	PageUp: { key: "PageUp", code: "PageUp", keyCode: 33 },
	PageDown: { key: "PageDown", code: "PageDown", keyCode: 34 },
	Insert: { key: "Insert", code: "Insert", keyCode: 45 },
	Control: { key: "Control", code: "ControlLeft", keyCode: 17 },
	Shift: { key: "Shift", code: "ShiftLeft", keyCode: 16 },
	Alt: { key: "Alt", code: "AltLeft", keyCode: 18 },
	Meta: { key: "Meta", code: "MetaLeft", keyCode: 91 },
};
for (let i = 1; i <= 12; i++) NAMED[`F${i}`] = { key: `F${i}`, code: `F${i}`, keyCode: 111 + i };

const ALIASES: Record<string, string> = {
	esc: "Escape",
	return: "Enter",
	up: "ArrowUp",
	down: "ArrowDown",
	left: "ArrowLeft",
	right: "ArrowRight",
	ctrl: "Control",
	control: "Control",
	cmd: "Meta",
	command: "Meta",
	meta: "Meta",
	option: "Alt",
	alt: "Alt",
	shift: "Shift",
	space: "Space",
	del: "Delete",
};

// Editing shortcuts need explicit commands; Chrome does not run them for
// synthesized key events on macOS.
const EDITING_COMMANDS: Record<string, string> = { a: "selectAll", c: "copy", v: "paste", x: "cut", z: "undo" };

function canonical(name: string): string {
	const alias = ALIASES[name.toLowerCase()];
	if (alias) return alias;
	const named = Object.keys(NAMED).find((key) => key.toLowerCase() === name.toLowerCase());
	return named ?? name;
}

function definitionFor(name: string, shift: boolean): KeyDefinition {
	const named = NAMED[name];
	if (named) return named;
	if ([...name].length !== 1) throw new BridgeError("INVALID_ARGUMENT", `Unknown key: ${name}`);
	const char = name;
	const upper = char.toUpperCase();
	if (/^[a-z]$/i.test(char)) {
		const text = shift ? upper : char.toLowerCase();
		return { key: text, code: `Key${upper}`, keyCode: upper.charCodeAt(0), text };
	}
	if (/^[0-9]$/.test(char)) return { key: char, code: `Digit${char}`, keyCode: char.charCodeAt(0), text: char };
	return { key: char, code: "", keyCode: 0, text: char };
}

export type KeyEvent = {
	type: "keyDown" | "rawKeyDown" | "keyUp";
	key: string;
	code: string;
	windowsVirtualKeyCode: number;
	modifiers: number;
	text?: string;
	unmodifiedText?: string;
	commands?: string[];
};

/** The CDP key events for one press of a key or chord, in dispatch order. */
export function keyEvents(chord: string): KeyEvent[] {
	const parts = chord
		.split("+")
		.map((part) => part.trim())
		.filter(Boolean)
		.map(canonical);
	if (parts.length === 0) throw new BridgeError("INVALID_ARGUMENT", "key is required");
	const keyName = parts[parts.length - 1]!;
	const modifiers = parts.slice(0, -1);
	for (const modifier of modifiers) {
		if (!(modifier in MODIFIER_BITS)) throw new BridgeError("INVALID_ARGUMENT", `Unknown modifier: ${modifier}`);
	}
	const bits = modifiers.reduce((sum, modifier) => sum | MODIFIER_BITS[modifier as Modifier], 0);
	const events: KeyEvent[] = [];
	let held = 0;
	for (const modifier of modifiers) {
		held |= MODIFIER_BITS[modifier as Modifier];
		const def = NAMED[modifier]!;
		events.push({ type: "rawKeyDown", key: def.key, code: def.code, windowsVirtualKeyCode: def.keyCode, modifiers: held });
	}
	const def = definitionFor(keyName, (bits & MODIFIER_BITS.Shift) !== 0);
	// Text is produced only without Control/Meta/Alt, as on a real keyboard.
	const producesText = def.text !== undefined && (bits & (MODIFIER_BITS.Control | MODIFIER_BITS.Meta | MODIFIER_BITS.Alt)) === 0;
	const shortcut = bits & ~MODIFIER_BITS.Shift;
	let command = shortcut === MODIFIER_BITS.Control || shortcut === MODIFIER_BITS.Meta ? EDITING_COMMANDS[def.key.toLowerCase()] : undefined;
	if ((bits & MODIFIER_BITS.Shift) !== 0) command = command === "undo" ? "redo" : undefined;
	events.push({
		type: producesText ? "keyDown" : "rawKeyDown",
		key: def.key,
		code: def.code,
		windowsVirtualKeyCode: def.keyCode,
		modifiers: bits,
		...(producesText ? { text: def.text, unmodifiedText: def.text } : {}),
		...(command ? { commands: [command] } : {}),
	});
	events.push({ type: "keyUp", key: def.key, code: def.code, windowsVirtualKeyCode: def.keyCode, modifiers: bits });
	for (const modifier of [...modifiers].reverse()) {
		held &= ~MODIFIER_BITS[modifier as Modifier];
		const mod = NAMED[modifier]!;
		events.push({ type: "keyUp", key: mod.key, code: mod.code, windowsVirtualKeyCode: mod.keyCode, modifiers: held });
	}
	return events;
}
