export type ShortcutChord = {
	key: string;
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

export const SET_ARTIFACT_ZOOM_ENGAGED_CHANNEL = "artifactZoom:setEngaged";
export const ARTIFACT_ZOOM_SHORTCUT_CHANNEL = "artifactZoom:shortcut";
export type ArtifactZoomAction = "in" | "out" | "reset";

export function artifactZoomAction(chord: Pick<ShortcutChord, "alt" | "code" | "ctrl" | "key" | "meta">, isMac: boolean): ArtifactZoomAction | undefined {
	const hasPrimaryModifier = isMac ? chord.meta && !chord.ctrl : chord.ctrl && !chord.meta;
	if (!hasPrimaryModifier || chord.alt) return undefined;
	if (chord.key === "=" || chord.key === "+" || chord.code === "NumpadAdd") return "in";
	if (chord.key === "-" || chord.key === "_" || chord.code === "NumpadSubtract") return "out";
	if (chord.key === "0" || chord.code === "Numpad0") return "reset";
	return undefined;
}

export type AppShortcutId =
	| "find-in-chat"
	| "add-selection-to-chat"
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
	| "split-shell-terminal"
	| "split-shell-terminal-down"
	| "dictate"
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
	customizable?: boolean;
};

export const SHORTCUT_CATEGORIES: readonly ShortcutCategory[] = ["General", "Navigation", "Session", "Panels"];

export function isSplitPaneShortcut(id: AppShortcutId): boolean {
	return id === "split-shell-terminal" || id === "split-shell-terminal-down";
}

export const APP_SHORTCUTS: readonly ShortcutDefinition[] = [
	{ id: "find-in-chat", label: "Find in chat", category: "Session" },
	{ id: "add-selection-to-chat", label: "Add selection to chat", category: "Session" },
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
	{ id: "split-shell-terminal", label: "New terminal on the right", category: "Session" },
	{ id: "split-shell-terminal-down", label: "New terminal below", category: "Session" },
	{ id: "close-shell-terminal", label: "Close tab", category: "Session" },
	{ id: "reopen-closed-tab", label: "Reopen closed tab", category: "Session" },
	{ id: "archive-session", label: "Archive session", category: "Session" },
	{ id: "open-in-editor", label: "Open in editor", category: "Session" },
	{ id: "copy-session-path", label: "Copy session path", category: "Session" },
	{ id: "model-picker", label: "Choose model", category: "Session" },
	{ id: "dictate", label: "Dictate", category: "Session" },
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
		case "find-in-chat":
			return [binding("f", primary)];
		case "add-selection-to-chat":
			return [binding("l", primary)];
		case "new-session":
			return [isMac ? binding("n", { meta: true }) : binding("n", { ctrl: true, shift: true })];
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
			return [isMac ? binding("d", { alt: true, shift: true, code: "KeyD" }) : binding("d", { ctrl: true, shift: true })];
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
		case "split-shell-terminal":
			return [isMac ? binding("d", { meta: true }) : binding("d", { ctrl: true, alt: true, shift: true })];
		case "split-shell-terminal-down":
			return [isMac ? binding("d", { meta: true, shift: true }) : binding("_", { ctrl: true, alt: true, shift: true })];
		case "dictate":
			return [isMac ? binding("d", { alt: true, code: "KeyD" }) : binding("d", { ctrl: true, alt: true })];
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

const SHIFTED_KEY: Record<string, string> = { "[": "{", "]": "}", "`": "~" };

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

const YIELDS_TO_FOCUSED_TERMINAL: ReadonlySet<AppShortcutId> = new Set<AppShortcutId>([
	"command-palette",
	"quick-open-file",
	"history-back",
	"history-forward",
	"new-shell-terminal",
	"close-shell-terminal",
	"toggle-sidebar",
	"keyboard-shortcuts",
]);

export function shortcutYieldsToTerminal(id: AppShortcutId, chord: ShortcutChord): boolean {
	if (id === "find-in-chat" || id === "add-selection-to-chat") return true;
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

const ACCELERATOR_KEYS: Record<string, string> = {
	ArrowUp: "Up",
	ArrowDown: "Down",
	ArrowLeft: "Left",
	ArrowRight: "Right",
	" ": "Space",
	"+": "Plus",
	Enter: "Return",
	Escape: "Escape",
	Tab: "Tab",
	PageUp: "PageUp",
	PageDown: "PageDown",
	Backspace: "Backspace",
	Delete: "Delete",
	Home: "Home",
	End: "End",
};

function acceleratorKey(binding: ShortcutBinding): string | undefined {
	const named = ACCELERATOR_KEYS[binding.key];
	if (named) return named;
	if (/^[\x21-\x7e]$/.test(binding.key)) return binding.key.toUpperCase();
	if (/^F([1-9]|1[0-9]|2[0-4])$/.test(binding.key)) return binding.key;
	return binding.code?.match(/^(?:Key|Digit)([A-Z0-9])$/)?.[1];
}

export function shortcutBindingAccelerator(binding: ShortcutBinding): string | undefined {
	const key = acceleratorKey(binding);
	if (!key) return undefined;
	return [binding.ctrl && "Control", binding.alt && "Alt", binding.shift && "Shift", binding.meta && "Command", key]
		.filter(Boolean)
		.join("+");
}

export const APP_MENU_COMMAND_IDS = [
	"new-session",
	"new-shell-terminal",
	"open-in-editor",
	"open-in-file-manager",
	"archive-session",
	"toggle-sidebar",
	"toggle-inspector",
	"history-back",
	"history-forward",
	"open-orchestrator",
	"toggle-notifications",
] as const satisfies readonly (AppShortcutId | "open-in-file-manager" | "toggle-notifications")[];
export type AppMenuCommandId = (typeof APP_MENU_COMMAND_IDS)[number];
export type AppMenuActionId = Exclude<AppMenuCommandId, AppShortcutId>;
export type AppMenuAvailability = Partial<Record<AppMenuCommandId, boolean>>;

export function isAppMenuActionId(id: AppMenuCommandId): id is AppMenuActionId {
	return id === "open-in-file-manager" || id === "toggle-notifications";
}

export const SET_APP_MENU_AVAILABILITY_CHANNEL = "app:set-menu-availability";
export const APP_MENU_ACTION_CHANNEL = "app:menu-action";

export const NEW_SESSION_SHORTCUT_CHANNEL = "app:new-session";
export const KEYBOARD_SHORTCUTS_HELP_CHANNEL = "app:keyboard-shortcuts-help";
export const QUIT_CONFIRM_REQUEST_CHANNEL = "app:quit-confirm-request";
export type QuitConfirmRequest = { keepsRunning: boolean };
export const QUIT_CONFIRM_RESULT_CHANNEL = "app:quit-confirm-result";
export const NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL = "app:new-shell-terminal";
export const CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL = "app:close-shell-terminal";
export const OPEN_SETTINGS_SHORTCUT_CHANNEL = "app:open-settings";
export const PREVIOUS_SESSION_SHORTCUT_CHANNEL = "app:previous-session";
export const NEXT_SESSION_SHORTCUT_CHANNEL = "app:next-session";
export const PREVIOUS_TAB_SHORTCUT_CHANNEL = "app:previous-tab";
export const NEXT_TAB_SHORTCUT_CHANNEL = "app:next-tab";
export const APP_SHORTCUT_CHANNEL = "app:shortcut";
export type AppShortcutEvent = { id: AppShortcutId; index?: number };

export function matchesNewSessionShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("new-session", chord, isMac);
}

export function matchesNewShellTerminalShortcut(chord: ShortcutChord, isMac: boolean): boolean {
	return matchesAppShortcut("new-shell-terminal", chord, isMac);
}

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
