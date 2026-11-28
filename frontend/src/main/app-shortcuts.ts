import {
	APP_SHORTCUT_CHANNEL,
	APP_SHORTCUTS,
	CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL,
	KEYBOARD_SHORTCUTS_HELP_CHANNEL,
	matchesAppShortcut,
	matchedByUserOverride,
	shortcutYieldsToTerminal,
	NEXT_SESSION_SHORTCUT_CHANNEL,
	NEXT_TAB_SHORTCUT_CHANNEL,
	NEW_SESSION_SHORTCUT_CHANNEL,
	NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL,
	OPEN_SETTINGS_SHORTCUT_CHANNEL,
	PREVIOUS_SESSION_SHORTCUT_CHANNEL,
	PREVIOUS_TAB_SHORTCUT_CHANNEL,
	terminalFontSizeDelta,
	TERMINAL_FONT_SIZE_SHORTCUT_CHANNEL,
	ARTIFACT_ZOOM_SHORTCUT_CHANNEL,
	artifactZoomAction,
	type AppShortcutEvent,
	type AppShortcutId,
	type KeybindingOverrides,
	type ShortcutChord,
} from "../shared/shortcuts";

// The slice of Electron's Input we read, plus the emitter shape. Declared
// locally so tests can supply a plain fake while WebContents still satisfies it.
type BeforeInput = {
	key: string;
	// Physical key (layout-independent), needed for chords whose character
	// shifts, e.g. Ctrl+Shift+` reports key "~" but code "Backquote". Optional so
	// test doubles need not supply it; Electron always does at runtime.
	code?: string;
	control: boolean;
	meta: boolean;
	shift: boolean;
	alt: boolean;
	type: string;
	isAutoRepeat?: boolean;
};

type BeforeInputContents = {
	on(
		event: "before-input-event",
		listener: (event: { preventDefault: () => void }, input: BeforeInput) => void,
	): unknown;
};

// Browser listeners handle their own page reloads; browser menu callbacks also
// handle force reload while focus is in the shell-painted address bar.
export function attachShellReloadGuard(
	contents: BeforeInputContents,
	isMac: boolean,
	hasBrowserTarget: () => boolean = () => false,
	isRecording: () => boolean = () => false,
): void {
	contents.on("before-input-event", (event, input) => {
		if (input.type !== "keyDown" || input.alt || isRecording()) return;
		const key = input.key.toLowerCase();
		const primary = isMac ? input.meta && !input.control : input.control && !input.meta;
		if ((key === "r" && primary && !hasBrowserTarget()) || (key === "f5" && !input.meta)) {
			event.preventDefault();
		}
	});
}

type ShortcutTargetContents = {
	focus: () => void;
	send: (channel: string, ...args: unknown[]) => void;
};

// Shortcuts that predate the generic channel keep their dedicated one; every
// other command is delivered as `{ id, index? }` on APP_SHORTCUT_CHANNEL.
const legacyShortcutChannels: Partial<Record<AppShortcutId, string>> = {
	"new-session": NEW_SESSION_SHORTCUT_CHANNEL,
	"new-shell-terminal": NEW_SHELL_TERMINAL_SHORTCUT_CHANNEL,
	"close-shell-terminal": CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL,
	"keyboard-shortcuts": KEYBOARD_SHORTCUTS_HELP_CHANNEL,
	"open-settings": OPEN_SETTINGS_SHORTCUT_CHANNEL,
	"previous-session": PREVIOUS_SESSION_SHORTCUT_CHANNEL,
	"next-session": NEXT_SESSION_SHORTCUT_CHANNEL,
	"previous-tab": PREVIOUS_TAB_SHORTCUT_CHANNEL,
	"next-tab": NEXT_TAB_SHORTCUT_CHANNEL,
};

// Handled in main itself rather than forwarded to the shell.
const mainHandledShortcuts: ReadonlySet<AppShortcutId> = new Set(["toggle-browser-devtools"]);

/** The IPC message that runs shortcut `id` in the shell, shared by chords and menu items. */
export function appShortcutDelivery(
	id: AppShortcutId,
	event: AppShortcutEvent = { id },
): { channel: string; payload: unknown[] } {
	const legacy = legacyShortcutChannels[id];
	return legacy ? { channel: legacy, payload: [] } : { channel: APP_SHORTCUT_CHANNEL, payload: [event] };
}

const appShortcutMatch = (
	chord: ShortcutChord,
	isMac: boolean,
	overrides: KeybindingOverrides,
): { id: AppShortcutId; channel: string; payload: unknown[] } | null => {
	for (const { id } of APP_SHORTCUTS) {
		if (mainHandledShortcuts.has(id) || !matchesAppShortcut(id, chord, isMac, overrides)) continue;
		const event: AppShortcutEvent = id === "jump-to-session" ? { id, index: Number(chord.key) - 1 } : { id };
		return { id, ...appShortcutDelivery(id, event) };
	}
	return null;
};

// JIS puts + on the shifted semicolon key and ^ in the US = position.
// On macOS the shifted semicolon key can be reported as ; or : with Command.
function isMacTerminalZoomInAlias(chord: ShortcutChord): boolean {
	if (!chord.meta || chord.ctrl || chord.alt) return false;
	if (chord.shift) return chord.code === "Semicolon" && (chord.key === ";" || chord.key === ":");
	return chord.code === "Equal" && chord.key === "^";
}

// Handle application-owned shortcuts in the main process so they work no
// matter which web contents holds focus, including xterm's helper textarea and
// the native Browser-preview WebContentsView.
export function attachAppShortcuts(
	contents: BeforeInputContents,
	isMac: boolean,
	target: ShortcutTargetContents,
	focusTarget = false,
	getOverrides: () => KeybindingOverrides = () => ({}),
	isRecording: () => boolean = () => false,
	shouldHandle: (id: AppShortcutId, chord: ShortcutChord) => boolean = () => true,
	onShortcut?: (id: AppShortcutId) => void,
	isTerminalFocused: () => boolean = () => false,
	// Only terminals that resize their own font claim the zoom chords; the rest
	// leave them to app zoom. Defaults to "any focused terminal" for callers that
	// do not distinguish.
	isTerminalFontSizeFocused: () => boolean = isTerminalFocused,
	// An artifact viewer that can zoom, under the pointer or holding focus,
	// claims the zoom chords ahead of the app's View zoom, even from a framed
	// page whose keys never reach the viewer itself.
	isArtifactZoomEngaged: () => boolean = () => false,
	// Told each chord this handler claims and runs, before the action is sent,
	// so the shell can count it by id.
	onPressed: (id: AppShortcutId) => void = () => {},
): void {
	contents.on("before-input-event", (event, input) => {
		if (input.type !== "keyDown") return;
		const chord = {
			key: input.key,
			code: input.code,
			ctrl: input.control,
			meta: input.meta,
			shift: input.shift,
			alt: input.alt,
		};
		let fontSizeDelta = terminalFontSizeDelta(chord, isMac);
		const zoomChord = fontSizeDelta !== 0 || (isMac && isMacTerminalZoomInAlias(chord));
		if (zoomChord && isTerminalFontSizeFocused()) {
			// The JIS aliases can overlap user bindings on other keyboard layouts.
			if (fontSizeDelta === 0 && !isRecording()) {
				const overrides = getOverrides();
				if (!APP_SHORTCUTS.some(({ id }) => matchesAppShortcut(id, chord, isMac, overrides))) {
					fontSizeDelta = 1;
				}
			}
			if (fontSizeDelta !== 0) {
				event.preventDefault();
				if (focusTarget) target.focus();
				target.send(TERMINAL_FONT_SIZE_SHORTCUT_CHANNEL, fontSizeDelta);
				return;
			}
		}
		const artifactZoom = artifactZoomAction(chord, isMac);
		if (artifactZoom && isArtifactZoomEngaged()) {
			event.preventDefault();
			target.send(ARTIFACT_ZOOM_SHORTCUT_CHANNEL, artifactZoom);
			return;
		}
		// Let the renderer's capture listener receive application-owned chords
		// while the user is recording a replacement binding.
		if (isRecording()) return;
		if (
			onShortcut &&
			!input.isAutoRepeat &&
			matchesAppShortcut("toggle-browser-devtools", chord, isMac, getOverrides()) &&
			shouldHandle("toggle-browser-devtools", chord)
		) {
			event.preventDefault();
			if (focusTarget) target.focus();
			onPressed("toggle-browser-devtools");
			onShortcut("toggle-browser-devtools");
			return;
		}
		const overrides = getOverrides();
		const match = appShortcutMatch(chord, isMac, overrides);
		if (!match) return;
		const { id, channel, payload } = match;
		// Contextual actions are owned by the focused surface. Let the renderer
		// inspect dialogs, editors, focus and the live selection before claiming them.
		if (id === "find-in-chat" || id === "add-selection-to-chat") return;
		if (
			!isMac &&
			shortcutYieldsToTerminal(id, chord, matchedByUserOverride(id, chord, isMac, overrides)) &&
			isTerminalFocused()
		) {
			return;
		}
		if (!shouldHandle(id, chord)) {
			// Browser context owns these chords. Consume them so a racing shell
			// listener cannot open/close a terminal from the same keypress, and so
			// Chromium does not treat ⌘T as an unhandled accelerator.
			if (id === "new-shell-terminal" || id === "close-shell-terminal") {
				event.preventDefault();
			}
			return;
		}

		event.preventDefault();
		// Consume repeats without re-firing: a held ⌘W must stay a single close,
		// never a fall-through to the menu's Close item.
		if (input.isAutoRepeat) return;
		if (focusTarget) target.focus();
		onPressed(id);
		target.send(channel, ...payload);
	});
}
