import { apiClient } from "../../lib/api-client";

// Keep in-flight writes ordered across tab switches and component remounts.
const writes = new Map<string, Promise<unknown>>();
function enqueue<T>(sessionId: string, sideId: string, operation: () => Promise<T>): Promise<T> {
	const key = JSON.stringify([sessionId, sideId]);
	const previous = writes.get(key) ?? Promise.resolve();
	const next = previous.catch(() => undefined).then(operation);
	writes.set(key, next);
	void next
		.finally(() => {
			if (writes.get(key) === next) writes.delete(key);
		})
		.catch(() => undefined);
	return next;
}
export function saveSideChatDraft(sessionId: string, sideId: string, contentJson: string) {
	return enqueue(sessionId, sideId, () =>
		apiClient.PUT("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
			params: { path: { sessionId, sideId } },
			body: { contentJson },
		}),
	);
}
export function readSideChatDraft(sessionId: string, sideId: string) {
	return enqueue(sessionId, sideId, () =>
		apiClient.GET("/api/v1/sessions/{sessionId}/conversation/side-chats/{sideId}/draft", {
			params: { path: { sessionId, sideId } },
		}),
	);
}
