// Pure, platform-parameterized shortcut matchers shared by the main process
// (Electron `before-input-event`) and any renderer code. Kept free of Electron
// and DOM types so it is trivially unit-testable and usable on both sides.

export type ShortcutChord = {
	key: string;
	// Physical key (KeyboardEvent.code / Electron input.code), independent of
	// layout and modifiers. Needed for chords whose character shifts — e.g.
	// Ctrl+Shift+` reports key "~" on a US layout but code "Backquote".
	code?: string;
	ctrl: boolean;
	meta: boolean;
	shift: boolean;
	alt: boolean;
};

export const SET_CLOSE_SHELL_TERMINAL_SHORTCUT_ENABLED_CHANNEL =
	"app:set-close-shell-terminal-shortcut-enabled";
export const SET_TERMINAL_FOCUSED_CHANNEL = "terminal:set-focused";
export const TERMINAL_FONT_SIZE_SHORTCUT_CHANNEL = "terminal:font-size-shortcut";

export function terminalFontSizeDelta(chord: ShortcutChord, isMac: boolean): -1 | 0 | 1 {
	const hasPrimaryModifier = isMac
		? chord.meta && !chord.ctrl
		: chord.ctrl && !chord.meta;
	if (!hasPrimaryModifier || chord.alt) return 0;
	if (chord.key === "+" || chord.key === "=" || chord.code === "NumpadAdd") return 1;
	if (chord.key === "-" || chord.code === "NumpadSubtract") return -1;
	return 0;
}

export type AppShortcutId =
	| "new-session"
	| "new-shell-terminal"
	| "close-shell-terminal"
	| "reopen-closed-tab"
	| "keyboard-shortcuts"
	| "command-palette"
	| "quick-open-file"
	| "open-settings"
	| "toggle-sidebar"
	| "toggle-inspector"
	| "toggle-terminal"
	| "toggle-diff"
	| "jump-to-session"
	| "previous-session"
	| "next-session"
	| "previous-attention-session"
	| "next-attention-session"
	| "history-back"
	| "history-forward"
	| "previous-tab"
	| "next-tab"
	| "archive-session"
	| "open-in-editor"
	| "copy-session-path"
	| "model-picker"
	| "open-orchestrator"
	| "toggle-browser-devtools";

export type ShortcutCategory = "General" | "Navigation" | "Session" | "Panels";

export type ShortcutBinding = {
	key: string;
	code?: string;
	ctrl: boolean;
	meta: boolean;
	shift: boolean;
	alt: boolean;
};

export type KeybindingOverrides = Partial<Record<AppShortcutId, readonly ShortcutBinding[]>>;

export type ShortcutDefinition = {
	id: AppShortcutId;
	label: string;
	category: ShortcutCategory;
	/** Indexed project selection is a family of nine bindings, not one command. */
	customizable?: boolean;
};

export const SHORTCUT_CATEGORIES: readonly ShortcutCategory[] = ["General", "Navigation", "Session", "Panels"];

// The user-facing shortcut catalog. Bindings live exclusively in
// defaultShortcutBindings so runtime matching and displayed labels cannot drift.
export const APP_SHORTCUTS: readonly ShortcutDefinition[] = [
	{ id: "new-session", label: "New session", category: "General" },
	{ id: "command-palette", label: "Open command palette", category: "General" },
	{ id: "quick-open-file", label: "Quick open file", category: "General" },
	{ id: "open-settings", label: "Open settings", category: "General" },
	{ id: "keyboard-shortcuts", label: "Show keyboard shortcuts", category: "General" },
	{ id: "previous-session", label: "Previous session", category: "Navigation" },
	{ id: "next-session", label: "Next session", category: "Navigation" },
	{ id: "jump-to-session", label: "Jump to session 1–9", category: "Navigation", customizable: false },
	{ id: "previous-attention-session", label: "Previous session needing attention", category: "Navigation" },
	{ id: "next-attention-session", label: "Next session needing attention", category: "Navigation" },
	{ id: "history-back", label: "Go back", category: "Navigation" },
	{ id: "history-forward", label: "Go forward", category: "Navigation" },
	{ id: "previous-tab", label: "Previous tab", category: "Navigation" },
	{ id: "next-tab", label: "Next tab", category: "Navigation" },
	{ id: "new-shell-terminal", label: "New terminal", category: "Session" },
	{ id: "close-shell-terminal", label: "Close tab", category: "Session" },
	{ id: "reopen-closed-tab", label: "Reopen closed tab", category: "Session" },
	{ id: "archive-session", label: "Archive session", category: "Session" },
	{ id: "open-in-editor", label: "Open in editor", category: "Session" },
	{ id: "copy-session-path", label: "Copy session path", category: "Session" },
	{ id: "model-picker", label: "Choose model", category: "Session" },
	{ id: "open-orchestrator", label: "Open orchestrator", category: "Session" },
	{ id: "toggle-sidebar", label: "Toggle sidebar", category: "Panels" },
	{ id: "toggle-inspector", label: "Toggle inspector", category: "Panels" },
	{ id: "toggle-terminal", label: "Toggle terminal", category: "Panels" },
	{ id: "toggle-diff", label: "Toggle diff", category: "Panels" },
	{ id: "toggle-browser-devtools", label: "Toggle browser DevTools", category: "Panels" },
];

const binding = (
	key: string,
	modifiers: Partial<Pick<ShortcutBinding, "ctrl" | "meta" | "shift" | "alt" | "code">>,
): ShortcutBinding => ({
	key,
	ctrl: false,
	meta: false,
	shift: false,
	alt: false,
	...modifiers,
});

export function defaultShortcutBindings(id: AppShortcutId, isMac: boolean): readonly ShortcutBinding[] {
	const primary = isMac ? { meta: true } : { ctrl: true };
	switch (id) {
		case "new-session":
			return [isMac ? binding("n", { meta: true }) : binding("n", { ctrl: true, shift: true })];
		// Plain Ctrl+T/W/B are readline/fzf/tmux keys, so on Windows/Linux they
		// yield to a focused terminal (see app-shortcuts.ts). The Shift variants
		// are never terminal input and work from anywhere.
		case "new-shell-terminal":
			return isMac
				? [binding("t", { meta: true })]
				: [binding("t", { ctrl: true }), binding("`", { ctrl: true, shift: true, code: "Backquote" })];
		case "close-shell-terminal":
			return isMac
				? [binding("w", { meta: true })]
				: [binding("w", { ctrl: true }), binding("w", { ctrl: true, shift: true })];
		case "reopen-closed-tab":
			return [binding("t", { ...primary, shift: true })];
		case "keyboard-shortcuts":
			return [binding("/", primary)];
		case "command-palette":
			return [binding("k", primary), binding("p", { ...primary, shift: true })];
		case "quick-open-file":
			return [binding("p", primary)];
		case "open-settings":
			return [binding(",", primary)];
		case "toggle-sidebar":
			return isMac
				? [binding("b", { meta: true })]
				: [binding("b", { ctrl: true }), binding("e", { ctrl: true, shift: true })];
		case "toggle-inspector":
			return isMac
				? [binding("b", { meta: true, shift: true }), binding("b", { meta: true, alt: true, code: "KeyB" })]
				: [binding("b", { ctrl: true, shift: true })];
		case "toggle-terminal":
			return isMac
				? [binding("`", { ctrl: true, code: "Backquote" }), binding("j", { meta: true })]
				: [binding("`", { ctrl: true, code: "Backquote" })];
		case "toggle-diff":
			return [binding("d", { ...primary, shift: true })];
		case "jump-to-session":
			return [binding("1-9", primary)];
		case "previous-session":
			return isMac
				? [binding("[", { meta: true, shift: true, code: "BracketLeft" }), binding("ArrowUp", { meta: true, alt: true })]
				: [binding("PageUp", { ctrl: true }), binding("[", { ctrl: true, shift: true, code: "BracketLeft" })];
		case "next-session":
			return isMac
				? [binding("]", { meta: true, shift: true, code: "BracketRight" }), binding("ArrowDown", { meta: true, alt: true })]
				: [binding("PageDown", { ctrl: true }), binding("]", { ctrl: true, shift: true, code: "BracketRight" })];
		case "previous-attention-session":
			return [binding("ArrowUp", { ...primary, alt: true, shift: true })];
		case "next-attention-session":
			return [binding("ArrowDown", { ...primary, alt: true, shift: true })];
		case "history-back":
			return [isMac ? binding("[", { meta: true, code: "BracketLeft" }) : binding("ArrowLeft", { alt: true })];
		case "history-forward":
			return [isMac ? binding("]", { meta: true, code: "BracketRight" }) : binding("ArrowRight", { alt: true })];
		case "previous-tab":
			return isMac
				? [binding("Tab", { ctrl: true, shift: true }), binding("ArrowLeft", { meta: true, alt: true })]
				: [binding("Tab", { ctrl: true, shift: true })];
		case "next-tab":
			return isMac
				? [binding("Tab", { ctrl: true }), binding("ArrowRight", { meta: true, alt: true })]
				: [binding("Tab", { ctrl: true })];
		case "archive-session":
			return [binding("a", { ...primary, shift: true })];
		case "open-in-editor":
			return [isMac ? binding("o", { meta: true }) : binding("o", { ctrl: true, shift: true })];
		case "copy-session-path":
			return [isMac ? binding("c", { meta: true, shift: true }) : binding("l", { ctrl: true, shift: true })];
		case "model-picker":
			return [binding("m", { ...primary, shift: true })];
		// Ctrl+Shift+O is Open in editor on Windows/Linux (plain Ctrl+O is
		// readline), so that platform takes G for "go to orchestrator".
		case "open-orchestrator":
			return [isMac ? binding("o", { meta: true, shift: true }) : binding("g", { ctrl: true, shift: true })];
		case "toggle-browser-devtools":
			return [isMac ? binding("i", { meta: true, alt: true }) : binding("i", { ctrl: true, shift: true })];
	}
}

export function effectiveShortcutBindings(
	id: AppShortcutId,
	isMac: boolean,
	overrides: KeybindingOverrides = {},
): readonly ShortcutBinding[] {
	return overrides[id] ?? defaultShortcutBindings(id, isMac);
}

function normalizedKey(key: string): string {
	if (key === "Up") return "arrowup";
	if (key === "Down") return "arrowdown";
	return key.toLowerCase();
}

// What the same physical key types with Shift on a US layout.
const SHIFTED_KEY: Record<string, string> = { "[": "{", "]": "}", "`": "~" };

// `code` is a physical position, so honoring it for an ordinary printable key
// would fire on whatever another layout puts there (Dvorak's physical "[" types
// "/", and ⌘/ must stay ⌘/). Fall back to the physical key only when the logical
// key cannot be compared to the binding: it is not one printable ASCII character
// (⌥B -> "∫", German "Ü", "Dead", "Unidentified"), or it is the US-shifted form
// of the bound key ("{" for "[").
function codeFallbackApplies(chordKey: string, candidateKey: string): boolean {
	if (!/^[\x21-\x7e]$/.test(chordKey)) return true;
	return SHIFTED_KEY[normalizedKey(candidateKey)] === chordKey;
}

export function matchesShortcutBinding(chord: ShortcutChord, candidate: ShortcutBinding): boolean {
	const keyMatches =
		candidate.key === "1-9"
			? /^[1-9]$/.test(chord.key)
			: normalizedKey(candidate.key) === normalizedKey(chord.key);
	const codeMatches =
		candidate.code !== undefined &&
		chord.code !== undefined &&
		normalizedKey(candidate.code) === normalizedKey(chord.code) &&
		codeFallbackApplies(chord.key, candidate.key);
	return (
		(keyMatches || codeMatches) &&
		chord.ctrl === candidate.ctrl &&
		chord.meta === candidate.meta &&
		chord.shift === candidate.shift &&
		chord.alt === candidate.alt
	);
}

/**
 * Application shortcuts are intercepted before the focused terminal or text
 * field sees them. Reject bindings that would consume ordinary input, terminal
 * control sequences, or common OS editing/window commands.
 */
export function shortcutBindingValidationError(binding: ShortcutBinding, isMac: boolean): string | null {
	if (
		["alt", "altgraph", "capslock", "control", "dead", "meta", "numlock", "process", "scrolllock", "shift", "unidentified"].includes(
			normalizedKey(binding.key),
		)
	) {
		return "Press a non-modifier key with Ctrl, Alt, or Cmd.";
	}
	if (!binding.ctrl && !binding.meta && !binding.alt) {
		return "Use Ctrl, Alt, or Cmd with another key.";
	}

	const key = normalizedKey(binding.key);
	if (binding.ctrl && !binding.meta && !binding.alt) {
		if (key === "c" || key === "v") {
			return "That shortcut is reserved for terminal copy, paste, or interrupt.";
		}
		if (!binding.shift && ["d", "z", "\\", "s", "q"].includes(key)) {
			return "That shortcut is reserved for terminal control.";
		}
	}

	if (isMac && binding.meta && !binding.ctrl && !binding.alt) {
		if (["a", "c", "h", "m", "q", "s", "v", "w", "x", "z"].includes(key)) {
			return "That shortcut is reserved by macOS or standard editing commands.";
		}
	}

	if (!isMac && binding.alt && !binding.ctrl && !binding.meta && key === "f4") {
		return "That shortcut is reserved for closing the window.";
	}

	return null;
}

// Plain Ctrl+letter chords on Windows/Linux are live terminal keystrokes
// (readline: Ctrl+K kill-line, Ctrl+P previous history). While a terminal has
// focus these commands yield to it; Cmd chords on macOS never reach the shell.
// Alt+←/→ (history) is readline/TUI word-jump, so it yields the same way.
// Only the bare Ctrl or bare Alt form yields: Shift variants (Ctrl+Shift+P/W/E,
// Ctrl+Shift+`) and AltGr (reported as Ctrl+Alt) are never readline input, so
// they stay live as the terminal-safe way to run the same command.
const YIELDS_TO_FOCUSED_TERMINAL: ReadonlySet<AppShortcutId> = new Set<AppShortcutId>([
	"command-palette", // Ctrl+K kill-line
	"quick-open-file", // Ctrl+P previous history
	"history-back", // Alt+Left word jump
	"history-forward", // Alt+Right word jump
	"new-shell-terminal", // Ctrl+T transpose-chars / fzf file picker
	"close-shell-terminal", // Ctrl+W unix-word-rubout
	"toggle-sidebar", // Ctrl+B backward-char / tmux prefix
	"keyboard-shortcuts", // Ctrl+/ readline undo
]);

/** True when `chord` for `id` must reach a focused terminal instead (Windows/Linux only). */
export function shortcutYieldsToTerminal(id: AppShortcutId, chord: ShortcutChord): boolean {
	return YIELDS_TO_FOCUSED_TERMINAL.has(id) && !chord.shift && !chord.meta && chord.ctrl !== chord.alt;
}

export function matchesAppShortcut(
	id: AppShortcutId,
	chord: ShortcutChord,
	isMac: boolean,
	overrides: KeybindingOverrides = {},
): boolean {
	return effectiveShortcutBindings(id, isMac, overrides).some((candidate) => matchesShortcutBinding(chord, candidate));
}

export function shortcutBindingKeys(binding: ShortcutBinding, isMac: boolean): readonly string[] {
	const keys: string[] = [];
	// macOS lists modifiers in Apple's order (⌃⌥⇧⌘); other platforms spell them out.
	if (isMac) {
		if (binding.ctrl) keys.push("⌃");
		if (binding.alt) keys.push("⌥");
		if (binding.shift) keys.push("⇧");
		if (binding.meta) keys.push("⌘");
	} else {
		if (binding.ctrl) keys.push("Ctrl");
		if (binding.meta) keys.push("Meta");
		if (binding.alt) keys.push("Alt");
		if (binding.shift) keys.push("Shift");
	}
	const labels: Record<string, string> = {
		ArrowUp: "↑",
		ArrowDown: "↓",
		ArrowLeft: "←",
		ArrowRight: "→",
		PageUp: "PageUp",
		PageDown: "PageDown",
		Tab: "Tab",
		Backquote: "`",
		BracketLeft: "[",
		BracketRight: "]",
	};
	keys.push(labels[binding.code ?? binding.key] ?? binding.key.toUpperCase());
	return keys;
}

export function shortcutBindingLabel(binding: ShortcutBinding, isMac: boolean): string {
	return shortcutBindingKeys(binding, isMac).join(isMac ? "" : "+");
}

// IPC channel the main process uses to tell the renderer shell to open the New
// Task flow. Lives here (not in main/) so the main process, preload, and
// renderer can all reference one constant without crossing bundle boundaries.
export const NEW_SESSION_SHORTCUT_CHANNEL = "app:new-session";
export const KEYBOARD_SHORTCUTS_HELP_CHANNEL = "app:keyboard-shortcuts-help";
// ⌘Q on macOS: main asks the shell to show the quit confirm, and the shell
// answers on the result channel with true (quit) or false (cancelled).
export const QUIT_CONFIRM_REQUEST_CHANNEL = "app:quit-confirm-request";
/** keepsRunning: quitting only closes the window; AO stays in the menu bar. */
export type QuitConfirmRequest = { keepsRunning: boolean };
export const QUIT_CONFIRM_RESULT_CHANNEL = "app:quit-confirm-result";
export const NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL = "app:new-shell-terminal";
export const CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL = "app:close-shell-terminal";
export const OPEN_SETTINGS_SHORTCUT_CHANNEL = "app:open-settings";
export const PREVIOUS_SESSION_SHORTCUT_CHANNEL = "app:previous-session";
export const NEXT_SESSION_SHORTCUT_CHANNEL = "app:next-session";
export const PREVIOUS_TAB_SHORTCUT_CHANNEL = "app:previous-tab";
export const NEXT_TAB_SHORTCUT_CHANNEL = "app:next-tab";
// Every other application shortcut travels on one channel as `{ id, index? }`
// so adding a command never needs a new preload method. `index` is the zero-based
// digit for indexed commands (jump-to-session).
export const APP_SHORTCUT_CHANNEL = "app:shortcut";
export type AppShortcutEvent = { id: AppShortcutId; index?: number };

// New session: ⌘N on macOS, Ctrl+Shift+N on Windows/Linux. Plain Ctrl+N is a
// live terminal keystroke (readline/vim "next line"), so the non-mac binding
// adds Shift to stay clear of the shell. Handled at the application level
// (main-process before-input-event) so it fires even when focus is inside
// xterm's helper textarea or a native Browser-preview WebContentsView.
export function matchesNewSessionShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("new-session", chord, isMac);
}

// Terminal tabs follow the desktop tab convention: ⌘T on macOS and Ctrl+T on
// Windows/Linux. Handled in the main process so xterm cannot swallow it.
export function matchesNewShellTerminalShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("new-shell-terminal", chord, isMac);
}

// Keyboard shortcut help: ⌘/ on macOS, Ctrl+/ on Windows/Linux. This is also
// handled at the application level so the terminal and Browser preview cannot
// swallow the command before the shell sees it.
export function matchesKeyboardShortcutsHelpShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("keyboard-shortcuts", chord, isMac);
}

export function matchesOpenSettingsShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("open-settings", chord, isMac);
}

export function matchesPreviousSessionShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("previous-session", chord, isMac);
}

export function matchesNextSessionShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("next-session", chord, isMac);
}

export function matchesPreviousTabShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("previous-tab", chord, isMac);
}

export function matchesNextTabShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("next-tab", chord, isMac);
}
