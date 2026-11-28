// The renderer's analytics opt-out as last applied by telemetry.ts, kept apart
// from it so modules that count state for later events (the onboarding funnel)
// can check it and be told of an opt-out without importing the PostHog client.

let optedOut = false;
const listeners = new Set<() => void>();

export function isAnalyticsOptedOut(): boolean {
	return optedOut;
}

/** Runs `listener` each time analytics is opted out, so state counted for later events can be dropped. */
export function onAnalyticsOptedOut(listener: () => void): () => void {
	listeners.add(listener);
	return () => {
		listeners.delete(listener);
	};
}

/** Called by applyAnalyticsOptOut only. */
export function setAnalyticsOptedOut(value: boolean): void {
	optedOut = value;
	if (!value) return;
	for (const listener of listeners) {
		try {
			listener();
		} catch {
			// One listener's failure must not stop the rest from clearing.
		}
	}
}
