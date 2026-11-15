import type { AppShortcutEvent } from "../../shared/shortcuts";

type Listener = (event: AppShortcutEvent) => void;

const listeners = new Set<Listener>();

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
