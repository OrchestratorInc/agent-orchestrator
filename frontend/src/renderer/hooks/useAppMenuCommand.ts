import { useEffect, useRef } from "react";
import {
	APP_MENU_COMMAND_IDS,
	type AppMenuActionId,
	type AppMenuAvailability,
	type AppMenuCommandId,
} from "../../shared/shortcuts";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";

// A command can have several owners (the session top bar and the board both
// offer Orchestrator), so it applies while any of them says so.
const owners = new Map<AppMenuCommandId, number>();
let reportQueued = false;
let lastReport = "";

function report(): void {
	reportQueued = false;
	const availability: AppMenuAvailability = {};
	for (const id of APP_MENU_COMMAND_IDS) availability[id] = (owners.get(id) ?? 0) > 0;
	const serialized = JSON.stringify(availability);
	if (serialized === lastReport) return;
	lastReport = serialized;
	// Optional: the web preview and a stale preload have no native menu to update.
	aoBridge.app.setMenuAvailability?.(availability);
}

// One commit can drop an old owner and add a new one (a session switch); the
// microtask reports the result once.
function queueReport(): void {
	if (reportQueued) return;
	reportQueued = true;
	queueMicrotask(report);
}

/** Enables native menu item `id` while this component is mounted and `available`. */
export function useAppMenuAvailable(id: AppMenuCommandId, available: boolean): void {
	useEffect(() => {
		if (!available) return;
		owners.set(id, (owners.get(id) ?? 0) + 1);
		queueReport();
		return () => {
			owners.set(id, (owners.get(id) ?? 0) - 1);
			queueReport();
		};
	}, [id, available]);
}

/** For session commands, which stand down under Settings like useSessionShortcut. */
export function useSessionMenuAvailable(id: AppMenuCommandId, available: boolean): void {
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	useAppMenuAvailable(id, available && !settingsOpen);
}

// In-renderer listeners, so the command palette can run a menu action the
// native menu delivers on its own channel.
const localMenuActionListeners = new Set<(action: AppMenuActionId) => void>();

/** Runs every handler registered for a menu action, as if its menu item were chosen. */
export function runAppMenuAction(action: AppMenuActionId): void {
	for (const listener of Array.from(localMenuActionListeners)) listener(action);
}

/** Runs `handler` when a native menu item with no keyboard shortcut is chosen. */
export function useAppMenuAction(id: AppMenuActionId, handler: () => void): void {
	const handlerRef = useRef(handler);
	handlerRef.current = handler;
	useEffect(() => {
		const listener = (action: AppMenuActionId) => {
			if (action === id) handlerRef.current();
		};
		localMenuActionListeners.add(listener);
		const unsubscribe = aoBridge.app.onMenuAction?.(listener);
		return () => {
			localMenuActionListeners.delete(listener);
			unsubscribe?.();
		};
	}, [id]);
}
