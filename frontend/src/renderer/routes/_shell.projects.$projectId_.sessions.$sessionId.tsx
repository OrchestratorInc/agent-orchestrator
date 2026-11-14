import { createFileRoute } from "@tanstack/react-router";
import { loadSessionView } from "../lib/session-view-chunk";
import { RouteErrorCapture } from "../components/RouteErrorCapture";

export const Route = createFileRoute("/_shell/projects/$projectId_/sessions/$sessionId")({
	loader: async () => {
		await loadSessionView();
	},
	pendingMs: Number.POSITIVE_INFINITY,
	errorComponent: RouteErrorCapture,
});
