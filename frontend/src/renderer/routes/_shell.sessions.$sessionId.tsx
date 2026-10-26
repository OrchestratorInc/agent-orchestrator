import { createFileRoute } from "@tanstack/react-router";
import { prefetchLocalSessionConversation } from "../lib/prefetch-session-conversation";
import { loadSessionView } from "../lib/session-view-chunk";

// SessionView renders from LocalSessionHost in the shell so switching between
// project sessions and scratchpads reuses one mounted instance. The route starts
// conversation loading and awaits its chunk while the previous page stays up.
// The host owns the error panel if the chunk fails.
export const Route = createFileRoute("/_shell/sessions/$sessionId")({
	loader: async ({ context, params }) => {
		prefetchLocalSessionConversation(context.queryClient, params.sessionId);
		await loadSessionView();
	},
	pendingMs: Number.POSITIVE_INFINITY,
	errorComponent: () => null,
});
