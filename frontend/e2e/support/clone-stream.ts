export const CLONE_PREPARE_ROUTES = /\/api\/v1\/projects\/clone\/prepare(\/stream)?$/;

export function cloneStream(...events: Record<string, unknown>[]) {
	return {
		status: 200,
		contentType: "text/event-stream",
		body: events.map((event) => `data: ${JSON.stringify(event)}\n\n`).join(""),
	};
}
