/** Matches both clone routes, so a spec can count every clone the renderer starts. */
export const CLONE_PREPARE_ROUTES = /\/api\/v1\/projects\/clone\/prepare(\/stream)?$/;

/**
 * A reply for POST /api/v1/projects/clone/prepare/stream: each event becomes
 * one server-sent `data:` frame, and the stream ends after the last.
 */
export function cloneStream(...events: Record<string, unknown>[]) {
	return {
		status: 200,
		contentType: "text/event-stream",
		body: events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join(""),
	};
}
