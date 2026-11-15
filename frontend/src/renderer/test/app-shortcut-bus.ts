import type { AppShortcutEvent } from "../../shared/shortcuts";

type Listener = (event: AppShortcutEvent) => void;

const listeners = new Set<Listener>();

/**
 * Stands in for the preload's `app.onShortcut`: components subscribe through the
 * bridge stub in `setup.ts`, and tests call `emit` the way the main process
 * does when it matches a chord.
 */
export const appShortcutBus = {
	subscribe(listener: Listener): () => void {
		listeners.add(listener);
		return () => {
			listeners.delete(listener);
		};
	},
	emit(event: AppShortcutEvent): void {
		for (const listener of [...listeners]) listener(event);
	},
	reset(): void {
		listeners.clear();
	},
};
