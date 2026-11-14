export interface CloudNotificationHint {
	source: "cloud";
	eventId: string;
	type: string;
	occurredAt: string;
	payload: Record<string, unknown>;
}

type Listener = (hint: CloudNotificationHint) => void;

const listeners = new Set<Listener>();

export function publishCloudNotificationHint(hint: CloudNotificationHint): void {
	listeners.forEach((listener) => listener(hint));
}

export function subscribeCloudNotificationHints(listener: Listener): () => void {
	listeners.add(listener);
	return () => listeners.delete(listener);
}
