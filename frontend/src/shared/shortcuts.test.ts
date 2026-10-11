import { describe, expect, it } from "vitest";
import {
	APP_SHORTCUTS,
	matchesAppShortcut,
	
	matchesKeyboardShortcutsHelpShortcut,
	matchesNextTabShortcut,
	matchesNextSessionShortcut,
	matchesNewSessionShortcut,
	matchesNewShellTerminalShortcut,
	matchesOpenSettingsShortcut,
	matchesPreviousSessionShortcut,
	matchesPreviousTabShortcut,
	defaultShortcutBindings,
	matchesShortcutBinding,
	shortcutBindingKeys,
	shortcutBindingLabel,
	shortcutBindingValidationError,
	type ShortcutChord,
} from "./shortcuts";

function chord(overrides: Partial<ShortcutChord> & { key: string }): ShortcutChord {
	return { ctrl: false, meta: false, shift: false, alt: false, ...overrides };
}

describe("matchesNewSessionShortcut", () => {
	it("matches ⌘N on macOS (either key case)", () => {
		expect(matchesNewSessionShortcut(chord({ key: "n", meta: true }), true)).toBe(true);
		expect(matchesNewSessionShortcut(chord({ key: "N", meta: true }), true)).toBe(true);
	});

	it("does not match plain Ctrl+N on macOS", () => {
		expect(matchesNewSessionShortcut(chord({ key: "n", ctrl: true }), true)).toBe(false);
	});

	it("matches Ctrl+Shift+N on Windows/Linux", () => {
		expect(matchesNewSessionShortcut(chord({ key: "N", ctrl: true, shift: true }), false)).toBe(true);
	});

	it("does not match plain Ctrl+N on Windows/Linux (reserved for the terminal)", () => {
		expect(matchesNewSessionShortcut(chord({ key: "n", ctrl: true }), false)).toBe(false);
	});

	it("does not match ⌘N on Windows/Linux", () => {
		expect(matchesNewSessionShortcut(chord({ key: "n", meta: true }), false)).toBe(false);
	});

	it("ignores other keys and extra modifiers", () => {
		expect(matchesNewSessionShortcut(chord({ key: "m", meta: true }), true)).toBe(false);
		expect(matchesNewSessionShortcut(chord({ key: "n", meta: true, alt: true }), true)).toBe(false);
		expect(matchesNewSessionShortcut(chord({ key: "n", ctrl: true, shift: true, alt: true }), false)).toBe(false);
		expect(matchesNewSessionShortcut(chord({ key: "n", ctrl: true, shift: true, meta: true }), false)).toBe(false);
	});
});

describe("matchesNewShellTerminalShortcut", () => {
	it("matches Command+T on macOS and Ctrl+T on Windows/Linux", () => {
		expect(matchesNewShellTerminalShortcut(chord({ key: "t", meta: true }), true)).toBe(true);
		expect(matchesNewShellTerminalShortcut(chord({ key: "T", ctrl: true }), false)).toBe(true);
	});

	it("rejects the wrong platform modifier, old backtick chord, and extra modifiers", () => {
		expect(matchesNewShellTerminalShortcut(chord({ key: "t", ctrl: true }), true)).toBe(false);
		expect(matchesNewShellTerminalShortcut(chord({ key: "t", meta: true }), false)).toBe(false);
		expect(matchesNewShellTerminalShortcut(chord({ key: "`", ctrl: true }), false)).toBe(false);
		expect(matchesNewShellTerminalShortcut(chord({ key: "t", ctrl: true, shift: true }), false)).toBe(false);
	});
});

describe("matchesKeyboardShortcutsHelpShortcut", () => {
	it("matches Ctrl+/ on Windows/Linux and Command+/ on macOS", () => {
		expect(matchesKeyboardShortcutsHelpShortcut(chord({ key: "/", ctrl: true }), false)).toBe(true);
		expect(matchesKeyboardShortcutsHelpShortcut(chord({ key: "/", meta: true }), true)).toBe(true);
	});

	it("rejects the wrong platform modifier and extra modifiers", () => {
		expect(matchesKeyboardShortcutsHelpShortcut(chord({ key: "/", meta: true }), false)).toBe(false);
		expect(matchesKeyboardShortcutsHelpShortcut(chord({ key: "/", ctrl: true }), true)).toBe(false);
		expect(matchesKeyboardShortcutsHelpShortcut(chord({ key: "/", ctrl: true, shift: true }), false)).toBe(false);
		expect(matchesKeyboardShortcutsHelpShortcut(chord({ key: "?", ctrl: true }), false)).toBe(false);
	});
});

describe("additional application shortcuts", () => {
	it("matches settings on each platform and rejects extra modifiers", () => {
		expect(matchesOpenSettingsShortcut(chord({ key: ",", meta: true }), true)).toBe(true);
		expect(matchesOpenSettingsShortcut(chord({ key: ",", ctrl: true }), false)).toBe(true);
		expect(matchesOpenSettingsShortcut(chord({ key: ",", ctrl: true, shift: true }), false)).toBe(false);
	});

	it("matches previous and next session on each platform", () => {
		expect(matchesPreviousSessionShortcut(chord({ key: "ArrowUp", meta: true, alt: true }), true)).toBe(true);
		expect(matchesPreviousSessionShortcut(chord({ key: "PageUp", ctrl: true }), false)).toBe(true);
		expect(matchesNextSessionShortcut(chord({ key: "ArrowDown", meta: true, alt: true }), true)).toBe(true);
		expect(matchesNextSessionShortcut(chord({ key: "PageDown", ctrl: true }), false)).toBe(true);
		expect(matchesNextSessionShortcut(chord({ key: "Down", ctrl: true, alt: true }), false)).toBe(false);
		expect(matchesNextSessionShortcut(chord({ key: "Down", ctrl: true }), false)).toBe(false);
	});

	it("matches Ctrl+Tab and Ctrl+Shift+Tab on each platform", () => {
		for (const isMac of [true, false]) {
			expect(matchesNextTabShortcut(chord({ key: "Tab", ctrl: true }), isMac)).toBe(true);
			expect(matchesPreviousTabShortcut(chord({ key: "Tab", ctrl: true, shift: true }), isMac)).toBe(true);
			expect(matchesNextTabShortcut(chord({ key: "Tab", ctrl: true, shift: true }), isMac)).toBe(false);
			expect(matchesPreviousTabShortcut(chord({ key: "Tab", ctrl: true }), isMac)).toBe(false);
		}
	});

	it("matches the terminal toggle on ⌃` and, on macOS, ⌘J", () => {
		const backquote = chord({ key: "`", code: "Backquote", ctrl: true });
		expect(matchesAppShortcut("toggle-terminal", backquote, true)).toBe(true);
		expect(matchesAppShortcut("toggle-terminal", backquote, false)).toBe(true);
		expect(matchesAppShortcut("toggle-terminal", chord({ key: "j", meta: true }), true)).toBe(true);
		expect(matchesAppShortcut("toggle-terminal", chord({ key: "j", ctrl: true }), false)).toBe(false);
		expect(matchesAppShortcut("toggle-terminal", { ...backquote, shift: true }, true)).toBe(false);
	});

	it("matches close terminal on each platform", () => {
		expect(matchesAppShortcut("close-shell-terminal", chord({ key: "w", meta: true }), true)).toBe(true);
		expect(matchesAppShortcut("close-shell-terminal", chord({ key: "w", ctrl: true }), false)).toBe(true);
		expect(matchesAppShortcut("close-shell-terminal", chord({ key: "w", meta: true }), false)).toBe(false);
	});
});

describe("default shortcuts", () => {
	// [id, chord] pairs a user can press on macOS. ⌘⇧[ reports key "{" with
	// code BracketLeft, so those chords carry the physical code like Electron does.
	it.each([
		["new-session", chord({ key: "n", meta: true })],
		["new-shell-terminal", chord({ key: "t", meta: true })],
		["close-shell-terminal", chord({ key: "w", meta: true })],
		["reopen-closed-tab", chord({ key: "T", meta: true, shift: true })],
		["keyboard-shortcuts", chord({ key: "/", meta: true })],
		["command-palette", chord({ key: "k", meta: true })],
		["command-palette", chord({ key: "P", meta: true, shift: true })],
		["quick-open-file", chord({ key: "p", meta: true })],
		["open-settings", chord({ key: ",", meta: true })],
		["toggle-sidebar", chord({ key: "b", meta: true })],
		["toggle-inspector", chord({ key: "b", code: "KeyB", meta: true, alt: true })],
		["toggle-terminal", chord({ key: "`", code: "Backquote", ctrl: true })],
		["toggle-diff", chord({ key: "D", meta: true, shift: true })],
		["toggle-inspector", chord({ key: "B", meta: true, shift: true })],
		["jump-to-session", chord({ key: "1", meta: true })],
		["jump-to-session", chord({ key: "9", meta: true })],
		["previous-session", chord({ key: "{", code: "BracketLeft", meta: true, shift: true })],
		["next-session", chord({ key: "}", code: "BracketRight", meta: true, shift: true })],
		["previous-session", chord({ key: "ArrowUp", meta: true, alt: true })],
		["next-session", chord({ key: "ArrowDown", meta: true, alt: true })],
		["previous-attention-session", chord({ key: "ArrowUp", meta: true, alt: true, shift: true })],
		["next-attention-session", chord({ key: "ArrowDown", meta: true, alt: true, shift: true })],
		["history-back", chord({ key: "[", code: "BracketLeft", meta: true })],
		["history-forward", chord({ key: "]", code: "BracketRight", meta: true })],
		["previous-tab", chord({ key: "Tab", ctrl: true, shift: true })],
		["next-tab", chord({ key: "Tab", ctrl: true })],
		["previous-tab", chord({ key: "ArrowLeft", meta: true, alt: true })],
		["next-tab", chord({ key: "ArrowRight", meta: true, alt: true })],
		["archive-session", chord({ key: "A", meta: true, shift: true })],
		["open-in-editor", chord({ key: "o", meta: true })],
		["copy-session-path", chord({ key: "C", meta: true, shift: true })],
		["model-picker", chord({ key: "M", meta: true, shift: true })],
	] as const)("macOS binds %s to its chord", (id, pressed) => {
		expect(matchesAppShortcut(id, pressed, true)).toBe(true);
	});

	it("tells ⌘[ (history) apart from ⌘⇧[ (previous session)", () => {
		const unshifted = chord({ key: "[", code: "BracketLeft", meta: true });
		const shifted = chord({ key: "{", code: "BracketLeft", meta: true, shift: true });
		expect(matchesAppShortcut("history-back", unshifted, true)).toBe(true);
		expect(matchesAppShortcut("previous-session", unshifted, true)).toBe(false);
		expect(matchesAppShortcut("previous-session", shifted, true)).toBe(true);
		expect(matchesAppShortcut("history-back", shifted, true)).toBe(false);
	});

	it("tells ⌘⌥↑ (session) apart from ⌘⌥⇧↑ (needing attention)", () => {
		const session = chord({ key: "ArrowUp", meta: true, alt: true });
		const attention = chord({ key: "ArrowUp", meta: true, alt: true, shift: true });
		expect(matchesAppShortcut("previous-session", session, true)).toBe(true);
		expect(matchesAppShortcut("previous-attention-session", session, true)).toBe(false);
		expect(matchesAppShortcut("previous-attention-session", attention, true)).toBe(true);
		expect(matchesAppShortcut("previous-session", attention, true)).toBe(false);
	});

	it("does not bind jump-to-session to 0 or to letters", () => {
		expect(matchesAppShortcut("jump-to-session", chord({ key: "0", meta: true }), true)).toBe(false);
		expect(matchesAppShortcut("jump-to-session", chord({ key: "a", meta: true }), true)).toBe(false);
	});

	it.each([true, false])("never gives two commands the same chord (macOS: %s)", (isMac) => {
		const seen = new Map<string, string>();
		for (const { id } of APP_SHORTCUTS) {
			for (const binding of defaultShortcutBindings(id, isMac)) {
				const signature = [
					binding.code ?? binding.key.toLowerCase(),
					binding.ctrl,
					binding.meta,
					binding.shift,
					binding.alt,
				].join("|");
				expect(seen.get(signature), `${id} collides with ${seen.get(signature)}`).toBeUndefined();
				seen.set(signature, id);
			}
		}
	});

	it("labels macOS chords in Apple's modifier order", () => {
		const [binding] = defaultShortcutBindings("next-attention-session", true);
		expect(shortcutBindingKeys(binding, true)).toEqual(["⌥", "⇧", "⌘", "↓"]);
		const [, alias] = defaultShortcutBindings("toggle-terminal", true);
		expect(shortcutBindingLabel(alias, true)).toBe("⌘J");
		const [backquote] = defaultShortcutBindings("toggle-terminal", true);
		expect(shortcutBindingKeys(backquote, true)).toEqual(["⌃", "`"]);
		const [history] = defaultShortcutBindings("history-back", false);
		expect(shortcutBindingLabel(history, false)).toBe("Alt+←");
	});
});

describe("shortcut catalog", () => {
	it("provides runtime defaults for every shortcut on each platform", () => {
		for (const shortcut of APP_SHORTCUTS) {
			expect(defaultShortcutBindings(shortcut.id, true).length).toBeGreaterThan(0);
			expect(defaultShortcutBindings(shortcut.id, false).length).toBeGreaterThan(0);
		}
	});

	it("uses a user override instead of the default binding", () => {
		const overrides = {
			"toggle-terminal": [chord({ key: "j", ctrl: true })],
		};

		expect(matchesAppShortcut("toggle-terminal", chord({ key: "j", ctrl: true }), false, overrides)).toBe(true);
		expect(matchesAppShortcut("toggle-terminal", chord({ key: "t", ctrl: true, shift: true }), false, overrides)).toBe(
			false,
		);
	});
});

describe("shortcut binding matching and validation", () => {
	it("matches either the logical key or physical code when both are available", () => {
		const candidate = chord({ key: "`", code: "Backquote", ctrl: true });

		expect(matchesShortcutBinding(chord({ key: "`", code: "IntlBackslash", ctrl: true }), candidate)).toBe(true);
		expect(matchesShortcutBinding(chord({ key: "§", code: "Backquote", ctrl: true }), candidate)).toBe(true);
	});

	it("requires a modifier and reserves terminal-critical control chords", () => {
		expect(shortcutBindingValidationError(chord({ key: "F6" }), false)).not.toBeNull();
		expect(shortcutBindingValidationError(chord({ key: "c", ctrl: true }), false)).not.toBeNull();
		expect(shortcutBindingValidationError(chord({ key: "v", ctrl: true, shift: true }), false)).not.toBeNull();
		expect(shortcutBindingValidationError(chord({ key: "d", ctrl: true }), false)).not.toBeNull();
		expect(shortcutBindingValidationError(chord({ key: "j", ctrl: true }), false)).toBeNull();
	});

	it("does not let a physical code override a different layout's printable key", () => {
		const historyForward = defaultShortcutBindings("history-forward", true)[0];
		const keyboardHelp = defaultShortcutBindings("keyboard-shortcuts", true)[0];
		// Dvorak: the physical "[" key types "/", the physical "]" key types "=".
		const dvorakCmdSlash = chord({ key: "/", code: "BracketLeft", meta: true });
		const dvorakCmdEqual = chord({ key: "=", code: "BracketRight", meta: true });
		expect(matchesShortcutBinding(dvorakCmdSlash, keyboardHelp)).toBe(true);
		expect(matchesAppShortcut("history-back", dvorakCmdSlash, true)).toBe(false);
		expect(matchesShortcutBinding(dvorakCmdEqual, historyForward)).toBe(false);
		// Dvorak ⌘] types "]" from a different physical key and still matches by key.
		expect(matchesShortcutBinding(chord({ key: "]", code: "Equal", meta: true }), historyForward)).toBe(true);
		// Non-mac Dvorak Ctrl+Shift+physical "[" is Ctrl+Shift+/, not previous-session.
		expect(
			matchesAppShortcut("previous-session", chord({ key: "?", code: "BracketLeft", ctrl: true, shift: true }), false),
		).toBe(false);
	});

	it("still matches bracket and backquote chords whose key is shifted or non-ASCII", () => {
		// US shifted forms.
		expect(matchesAppShortcut("previous-session", chord({ key: "{", code: "BracketLeft", ctrl: true, shift: true }), false)).toBe(true);
		expect(matchesAppShortcut("next-session", chord({ key: "}", code: "BracketRight", ctrl: true, shift: true }), false)).toBe(true);
		expect(matchesAppShortcut("new-shell-terminal", chord({ key: "~", code: "Backquote", ctrl: true, shift: true }), false)).toBe(true);
		// German: physical "[" is Ü (shifted Ü), physical "]" is + / *, backquote is a dead "^".
		expect(matchesAppShortcut("previous-session", chord({ key: "Ü", code: "BracketLeft", meta: true, shift: true }), true)).toBe(true);
		expect(matchesAppShortcut("history-back", chord({ key: "ü", code: "BracketLeft", meta: true }), true)).toBe(true);
		expect(matchesAppShortcut("toggle-terminal", chord({ key: "Dead", code: "Backquote", ctrl: true }), false)).toBe(true);
		// Option+B types "∫" on macOS, so the Meta+Alt+B alias needs the code.
		expect(matchesAppShortcut("toggle-inspector", chord({ key: "∫", code: "KeyB", meta: true, alt: true }), true)).toBe(true);
	});

	it("never matches plain letter or digit chords by physical code", () => {
		// AZERTY: physical KeyQ types "a"; physical KeyW types "z". Ctrl+physical-Q
		// is Ctrl+A to the user and must not fire anything bound to Q.
		for (const isMac of [true, false]) {
			for (const { id } of APP_SHORTCUTS) {
				for (const bound of defaultShortcutBindings(id, isMac)) {
					if (/^[a-z0-9]$/i.test(bound.key)) {
						expect(bound.code === undefined || bound.code === "KeyB", `${id} carries code ${bound.code}`).toBe(true);
					}
				}
			}
		}
		const sidebar = defaultShortcutBindings("toggle-sidebar", false)[0];
		expect(matchesShortcutBinding(chord({ key: "x", code: "KeyB", ctrl: true }), sidebar)).toBe(false);
	});

	it("never fires a Windows/Linux default from an AltGr character (reported as Ctrl+Alt)", () => {
		// [key, code] AltGr outputs on German, French, Spanish, Polish and Brazilian layouts.
		const altGr: Array<[string, string]> = [
			["@", "KeyQ"], ["{", "Digit7"], ["}", "Digit0"], ["[", "Digit8"], ["]", "Digit9"], ["\\", "Minus"],
			["|", "IntlBackslash"], ["~", "BracketRight"], ["€", "KeyE"], ["µ", "KeyM"],
			["`", "Quote"], ["#", "Digit3"], ["ł", "KeyL"], ["ę", "KeyE"], ["ą", "KeyA"], ["ś", "KeyS"], ["ż", "KeyZ"],
			["ź", "KeyX"], ["ć", "KeyC"], ["ń", "KeyN"], ["ó", "KeyO"], ["/", "KeyQ"], ["?", "KeyW"], ["¢", "KeyC"],
			["b", "KeyB"], ["i", "KeyI"], ["t", "KeyT"], ["w", "KeyW"], ["p", "KeyP"], ["1", "Digit1"],
		];
		for (const [key, code] of altGr) {
			for (const shift of [false, true]) {
				const pressed = chord({ key, code, ctrl: true, alt: true, shift });
				for (const { id } of APP_SHORTCUTS) {
					expect(matchesAppShortcut(id, pressed, false), `${id} fired from AltGr ${key}`).toBe(false);
				}
			}
		}
	});

	it("keeps Windows/Linux defaults off bare terminal-owned chords other than the yielding ones", () => {
		const readline = ["a", "e", "k", "u", "w", "r", "p", "n", "b", "f", "l", "y", "t", "d", "c", "z"];
		const yielding = new Set(["k", "p", "w", "t", "b"]);
		for (const key of readline) {
			if (yielding.has(key)) continue;
			for (const { id } of APP_SHORTCUTS) {
				expect(matchesAppShortcut(id, chord({ key, ctrl: true }), false), `${id} steals Ctrl+${key}`).toBe(false);
			}
		}
		for (const key of ["b", "f", "d", "Backspace"]) {
			for (const { id } of APP_SHORTCUTS) {
				expect(matchesAppShortcut(id, chord({ key, alt: true }), false), `${id} steals Alt+${key}`).toBe(false);
			}
		}
		for (const key of ["ArrowLeft", "ArrowRight", "Backspace"]) {
			for (const { id } of APP_SHORTCUTS) {
				expect(matchesAppShortcut(id, chord({ key, ctrl: true }), false), `${id} steals Ctrl+${key}`).toBe(false);
			}
		}
	});

	it("does not bind terminal copy/paste or Linux unicode entry chords on Windows/Linux", () => {
		for (const { id } of APP_SHORTCUTS) {
			for (const bound of defaultShortcutBindings(id, false)) {
				if (bound.ctrl && bound.shift && !bound.alt && !bound.meta) {
					expect(["c", "v", "u"], `${id} binds Ctrl+Shift+${bound.key}`).not.toContain(bound.key.toLowerCase());
				}
				expect(bound.alt && bound.shift && !bound.ctrl && !bound.meta, `${id} binds Alt+Shift (layout switch)`).toBe(false);
			}
		}
		// copy-session-path must not be Ctrl+Shift+C.
		expect(matchesAppShortcut("copy-session-path", chord({ key: "C", ctrl: true, shift: true }), false)).toBe(false);
	});

	it("gives terminal-safe Shift alternatives for yielding commands on Windows/Linux", () => {
		expect(matchesAppShortcut("close-shell-terminal", chord({ key: "W", ctrl: true, shift: true }), false)).toBe(true);
		expect(matchesAppShortcut("new-shell-terminal", chord({ key: "~", code: "Backquote", ctrl: true, shift: true }), false)).toBe(true);
		expect(matchesAppShortcut("toggle-sidebar", chord({ key: "E", ctrl: true, shift: true }), false)).toBe(true);
		expect(matchesAppShortcut("toggle-inspector", chord({ key: "B", ctrl: true, shift: true }), false)).toBe(true);
		expect(matchesAppShortcut("toggle-inspector", chord({ key: "E", ctrl: true, shift: true }), false)).toBe(false);
		// macOS bindings are unchanged.
		expect(defaultShortcutBindings("new-shell-terminal", true)).toHaveLength(1);
		expect(defaultShortcutBindings("close-shell-terminal", true)).toHaveLength(1);
	});

	it("reserves common platform window and editing chords", () => {
		expect(shortcutBindingValidationError(chord({ key: "q", meta: true }), true)).not.toBeNull();
		expect(shortcutBindingValidationError(chord({ key: "F4", alt: true }), false)).not.toBeNull();
		expect(shortcutBindingValidationError(chord({ key: "j", meta: true }), true)).toBeNull();
	});
});
