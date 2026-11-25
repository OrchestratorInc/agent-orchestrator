// Moments the guided tour (GuidedTour) waits for that the page doesn't show
// reliably: a chat's composer delivering a message. Any chat counts.

const sentListeners = new Set<() => void>();

export function notifyComposerSent() {
	for (const listener of sentListeners) listener();
}

export function onComposerSent(listener: () => void) {
	sentListeners.add(listener);
	return () => {
		sentListeners.delete(listener);
	};
}
