import { useEffect, useRef } from "react";
import {
	APP_MENU_COMMAND_IDS,
	type AppMenuActionId,
	type AppMenuAvailability,
	type AppMenuCommandId,
} from "../../shared/shortcuts";
import { aoBridge } from "../lib/bridge";
import { useUiStore } from "../stores/ui-store";

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
	aoBridge.app.setMenuAvailability?.(availability);
}

function queueReport(): void {
	if (reportQueued) return;
	reportQueued = true;
	queueMicrotask(report);
}

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

export function useSessionMenuAvailable(id: AppMenuCommandId, available: boolean): void {
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	useAppMenuAvailable(id, available && !settingsOpen);
}

const localMenuActionListeners = new Set<(action: AppMenuActionId) => void>();

export function runAppMenuAction(action: AppMenuActionId): void {
	for (const listener of Array.from(localMenuActionListeners)) listener(action);
}

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
