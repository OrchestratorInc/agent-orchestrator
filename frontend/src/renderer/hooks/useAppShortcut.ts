import { useEffect, useRef } from "react";
import {
	matchedByUserOverride,
	matchesAppShortcut,
	shortcutYieldsToTerminal,
	type AppShortcutEvent,
	type AppShortcutId,
} from "../../shared/shortcuts";
import { aoBridge } from "../lib/bridge";
import { queryShown } from "../lib/dom-selectors";
import { isMacPlatform } from "../lib/platform";
import { uiUsage } from "../lib/ui-usage-telemetry";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { useUiStore } from "../stores/ui-store";

/** Focuses the first shown element matching `selector` once the next render has painted. */
export function focusAfterPaint(selector: string): void {
	window.requestAnimationFrame(() => {
		window.requestAnimationFrame(() => queryShown<HTMLElement>(selector)?.focus());
	});
}

// Keydown events this hook already claimed. Several components can subscribe to
// the same shortcut (the shell and Settings both take Cmd+1-9), and the first one
// calls preventDefault, which must not stop the rest from running.
const claimedEvents = new WeakSet<Event>();

// In-renderer listeners, so the command palette can run a shortcut's action
// exactly as its chord would.
const localListeners = new Set<(event: AppShortcutEvent) => void>();

/** Runs every handler registered for event.id, as if its chord had been pressed. */
export function runAppShortcut(event: AppShortcutEvent): void {
	for (const listener of Array.from(localListeners)) listener(event);
}

/**
 * For shortcuts main delivers on a channel of their own (⌘N, session and tab
 * cycling, New terminal, Close tab): subscribes `handler` there and to the
 * command palette running the shortcut, which only reaches in-renderer
 * listeners. Returns the unsubscribe.
 */
export function onDedicatedShortcut(
	id: AppShortcutId,
	subscribe: (handler: () => void) => () => void,
	handler: () => void,
): () => void {
	const unsubscribe = subscribe(handler);
	const local = (event: AppShortcutEvent) => {
		if (event.id === id) handler();
	};
	localListeners.add(local);
	return () => {
		unsubscribe();
		localListeners.delete(local);
	};
}

const TERMINAL_SELECTOR = ".xterm, .terminal-surface, [data-testid='session-terminal']";

/**
 * Runs `handler` when shortcut `id` is pressed.
 *
 * In the desktop app the main process matches the chord before xterm or a
 * native browser view can swallow it, so this works wherever focus is. A window
 * keydown listener backs that up where no main process is attached (the web
 * preview, e2e). It never double-fires: main consumes the chords it handles, so
 * the page does not see them, and a handler that already ran has called
 * preventDefault. Windows/Linux chords that belong to a focused terminal
 * (Ctrl+K and friends) are left alone, matching main.
 *
 * Pass `enabled: false` to stop listening (e.g. while the owning surface is not
 * the active one).
 */
export function useAppShortcut(
	id: AppShortcutId,
	handler: (event: AppShortcutEvent) => void,
	enabled = true,
	shouldHandle: () => boolean = () => true,
): void {
	const handlerRef = useRef(handler);
	handlerRef.current = handler;
	const shouldHandleRef = useRef(shouldHandle);
	shouldHandleRef.current = shouldHandle;
	useEffect(() => {
		if (!enabled) return;
		const listener = (event: AppShortcutEvent) => {
			if (event.id === id && shouldHandleRef.current()) handlerRef.current(event);
		};
		localListeners.add(listener);
		// Contextual find is deliberately renderer-owned: main leaves its key
		// event untouched so focused terminals, editors and dialogs can claim it.
		const unsubscribe = id === "find-in-chat" ? () => {} : aoBridge.app.onShortcut(listener);
		return () => {
			localListeners.delete(listener);
			unsubscribe();
		};
	}, [id, enabled]);
	useEffect(() => {
		if (!enabled) return;
		const isMac = isMacPlatform();
		const onKeyDown = (event: KeyboardEvent, beforeTerminal = false) => {
			if ((event.defaultPrevented && !claimedEvents.has(event)) || event.isComposing || event.repeat) return;
			const chord = {
				key: event.key,
				code: event.code,
				ctrl: event.ctrlKey,
				meta: event.metaKey,
				shift: event.shiftKey,
				alt: event.altKey,
			};
			const overrides = useKeybindingsStore.getState().overrides;
			if (!matchesAppShortcut(id, chord, isMac, overrides)) return;
			if (!shouldHandleRef.current()) return;
			const target = event.target instanceof Element ? event.target : null;
			const userOverrode = matchedByUserOverride(id, chord, isMac, overrides);
			// xterm consumes Ctrl+letter keydowns before they can bubble to the
			// window. Claim a custom contextual Find here only when a chat can
			// handle it. Defaults, IME and ineligible contexts still reach xterm.
			if (beforeTerminal && (!userOverrode || !target?.closest(".xterm"))) return;
			if (
				!isMac &&
				shortcutYieldsToTerminal(id, chord, userOverrode) &&
				target?.closest(TERMINAL_SELECTOR)
			) {
				return;
			}
			// The chords main leaves to the page (find, add selection) and, with no
			// main process, all of them. Counted once however many handlers claim it.
			if (!claimedEvents.has(event)) uiUsage.shortcutUsed(id, "key");
			claimedEvents.add(event);
			event.preventDefault();
			if (beforeTerminal) event.stopPropagation();
			handlerRef.current(id === "jump-to-session" ? { id, index: Number(event.key) - 1 } : { id });
		};
		const bubble = (event: KeyboardEvent) => onKeyDown(event);
		const capture = (event: KeyboardEvent) => onKeyDown(event, true);
		window.addEventListener("keydown", bubble);
		if (id === "find-in-chat") window.addEventListener("keydown", capture, true);
		return () => {
			window.removeEventListener("keydown", bubble);
			if (id === "find-in-chat") window.removeEventListener("keydown", capture, true);
		};
	}, [id, enabled]);
}

/** True while the global Settings page covers the routed (session) page. */
export function isSettingsPageOpen(): boolean {
	return useUiStore.getState().settingsModal?.scope === "global";
}

/** True while the archive modal covers the routed page. */
function isArchiveModalOpen(): boolean {
	return Boolean(useUiStore.getState().archiveModal);
}

/**
 * For commands that act on the session on screen (archive, open in editor,
 * copy path, panel toggles). The session page stays mounted but hidden under
 * Settings or the archive modal, so these stand down there instead of acting
 * on a session the user cannot see.
 */
export function useSessionShortcut(
	id: AppShortcutId,
	handler: (event: AppShortcutEvent) => void,
	enabled = true,
): void {
	useAppShortcut(
		id,
		(event) => {
			if (!isSettingsPageOpen() && !isArchiveModalOpen()) handler(event);
		},
		enabled,
	);
}
