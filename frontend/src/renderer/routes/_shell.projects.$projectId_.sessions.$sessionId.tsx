import { createFileRoute } from "@tanstack/react-router";
import { loadSessionView } from "../lib/session-view-chunk";

// SessionView renders from LocalSessionHost in the shell so switching between
// project sessions and scratchpads reuses one mounted instance. The route only
// loads its chunk: with no pending screen the previous page stays up until the
// session can paint, and the host owns the error panel if the chunk fails.
export const Route = createFileRoute("/_shell/projects/$projectId_/sessions/$sessionId")({
	loader: async () => {
		await loadSessionView();
	},
	pendingMs: Number.POSITIVE_INFINITY,
	errorComponent: () => null,
});
