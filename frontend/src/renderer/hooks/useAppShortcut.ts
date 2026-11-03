import { useEffect, useRef } from "react";
import { matchesAppShortcut, shortcutYieldsToTerminal, type AppShortcutEvent, type AppShortcutId } from "../../shared/shortcuts";
import { aoBridge } from "../lib/bridge";
import { isMacPlatform } from "../lib/platform";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { useUiStore } from "../stores/ui-store";

/** Focuses the first element matching `selector` once the next render has painted. */
export function focusAfterPaint(selector: string): void {
	window.requestAnimationFrame(() => {
		window.requestAnimationFrame(() => document.querySelector<HTMLElement>(selector)?.focus());
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
): void {
	const handlerRef = useRef(handler);
	handlerRef.current = handler;
	useEffect(() => {
		if (!enabled) return;
		const listener = (event: AppShortcutEvent) => {
			if (event.id === id) handlerRef.current(event);
		};
		localListeners.add(listener);
		const unsubscribe = aoBridge.app.onShortcut(listener);
		return () => {
			localListeners.delete(listener);
			unsubscribe();
		};
	}, [id, enabled]);
	useEffect(() => {
		if (!enabled) return;
		const isMac = isMacPlatform();
		const onKeyDown = (event: KeyboardEvent) => {
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
			const target = event.target instanceof Element ? event.target : null;
			if (!isMac && shortcutYieldsToTerminal(id, chord) && target?.closest(TERMINAL_SELECTOR)) return;
			claimedEvents.add(event);
			event.preventDefault();
			handlerRef.current(id === "jump-to-session" ? { id, index: Number(event.key) - 1 } : { id });
		};
		window.addEventListener("keydown", onKeyDown);
		return () => window.removeEventListener("keydown", onKeyDown);
	}, [id, enabled]);
}

/** True while the global Settings page covers the routed (session) page. */
export function isSettingsPageOpen(): boolean {
	return useUiStore.getState().settingsModal?.scope === "global";
}

/**
 * For commands that act on the session on screen (archive, open in editor,
 * copy path, panel toggles). The session page stays mounted but hidden under
 * Settings, so these stand down there instead of acting on an invisible session.
 */
export function useSessionShortcut(
	id: AppShortcutId,
	handler: (event: AppShortcutEvent) => void,
	enabled = true,
): void {
	useAppShortcut(
		id,
		(event) => {
			if (!isSettingsPageOpen()) handler(event);
		},
		enabled,
	);
}
