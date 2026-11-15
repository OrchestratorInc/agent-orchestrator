import { useEffect, useRef } from "react";
import { matchesAppShortcut, shortcutYieldsToTerminal, type AppShortcutEvent, type AppShortcutId } from "../../shared/shortcuts";
import { aoBridge } from "../lib/bridge";
import { isMacPlatform } from "../lib/platform";
import { useKeybindingsStore } from "../stores/keybindings-store";
import { useUiStore } from "../stores/ui-store";

export function focusAfterPaint(selector: string): void {
	window.requestAnimationFrame(() => {
		window.requestAnimationFrame(() => document.querySelector<HTMLElement>(selector)?.focus());
	});
}

const claimedEvents = new WeakSet<Event>();

const localListeners = new Set<(event: AppShortcutEvent) => void>();

export function runAppShortcut(event: AppShortcutEvent): void {
	for (const listener of Array.from(localListeners)) listener(event);
}

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
		const unsubscribe = id === "find-in-chat" ? () => {} : aoBridge.app.onShortcut(listener);
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
			if (!shouldHandleRef.current()) return;
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

export function isSettingsPageOpen(): boolean {
	return useUiStore.getState().settingsModal?.scope === "global";
}

function isArchiveModalOpen(): boolean {
	return Boolean(useUiStore.getState().archiveModal);
}

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
