import {
	APP_SHORTCUT_CHANNEL,
	APP_SHORTCUTS,
	CLOSE_SHELL_TERMINAL_SHORTCUT_CHANNEL,
	KEYBOARD_SHORTCUTS_HELP_CHANNEL,
	matchesAppShortcut,
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

type BeforeInput = {
	key: string;
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

const mainHandledShortcuts: ReadonlySet<AppShortcutId> = new Set(["toggle-browser-devtools"]);

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
	isTerminalFontSizeFocused: () => boolean = isTerminalFocused,
	isArtifactZoomEngaged: () => boolean = () => false,
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
		const fontSizeDelta = terminalFontSizeDelta(chord, isMac);
		if (fontSizeDelta !== 0 && isTerminalFontSizeFocused()) {
			event.preventDefault();
			if (focusTarget) target.focus();
			target.send(TERMINAL_FONT_SIZE_SHORTCUT_CHANNEL, fontSizeDelta);
			return;
		}
		const artifactZoom = artifactZoomAction(chord, isMac);
		if (artifactZoom && isArtifactZoomEngaged()) {
			event.preventDefault();
			target.send(ARTIFACT_ZOOM_SHORTCUT_CHANNEL, artifactZoom);
			return;
		}
		if (isRecording()) return;
		if (
			onShortcut &&
			!input.isAutoRepeat &&
			matchesAppShortcut("toggle-browser-devtools", chord, isMac, getOverrides()) &&
			shouldHandle("toggle-browser-devtools", chord)
		) {
			event.preventDefault();
			if (focusTarget) target.focus();
			onShortcut("toggle-browser-devtools");
			return;
		}
		const match = appShortcutMatch(chord, isMac, getOverrides());
		if (!match) return;
		const { id, channel, payload } = match;
		if (id === "find-in-chat" || id === "add-selection-to-chat") return;
		if (!isMac && shortcutYieldsToTerminal(id, chord) && isTerminalFocused()) return;
		if (!shouldHandle(id, chord)) {
			if (id === "new-shell-terminal" || id === "close-shell-terminal") {
				event.preventDefault();
			}
			return;
		}

		event.preventDefault();
		if (input.isAutoRepeat) return;
		if (focusTarget) target.focus();
		target.send(channel, ...payload);
	});
}
