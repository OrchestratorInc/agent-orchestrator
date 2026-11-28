import type { ToastMarker } from "../components/ui/toast-art";
import type { QueryClient } from "@tanstack/react-query";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { AgentProvider, WorkspaceSummary } from "../types/workspace";
import { useUiStore } from "../stores/ui-store";

/** What a toast needs from a bell notification, whichever inbox it came from. */
export type IncomingNotification = {
	id: string;
	type: string;
	title: string;
	body?: string;
	/** A remote host's name, shown before the title as in the bell. */
	hostLabel?: string;
	/** The agent of the session it is about, drawn on the tile. */
	provider?: AgentProvider;
};

/** How long a notification stays up; pointing at the deck pauses it. */
const NOTIFICATION_TOAST_MS = 8_000;

/**
 * Shows a notification from the bell as a toast the moment it arrives: the
 * daemon's title and body, the session's agent with the sidebar's mark for
 * what happened; pressing the card opens it. The bell keeps it; the toast only announces it.
 */
export function showNotificationToast(notification: IncomingNotification, open: () => void, durationMs = NOTIFICATION_TOAST_MS) {
	const title = notification.hostLabel ? `${notification.hostLabel}: ${notification.title}` : notification.title;
	useUiStore.getState().showGlobalToast(title, notification.body || undefined, {
		dedupeKey: `notification:${notification.id}`,
		durationMs,
		tone: notificationTone(notification.type),
		art: { kind: "tile", marker: notificationMarker(notification.type), provider: notification.provider },
		onOpen: open,
	});
}

function notificationTone(type: string): "info" | "success" | "error" {
	switch (type) {
		case "agent_failed":
		case "pr_closed_unmerged":
			return "error";
		case "ready_to_merge":
		case "review_completed":
		case "agent_completed":
		case "pr_merged":
			return "success";
		default:
			return "info";
	}
}

/** The sidebar's mark for each kind of event, so the toast and the session's row agree. */
export function notificationMarker(type: string): ToastMarker {
	switch (type) {
		case "needs_input":
			return "input";
		case "review_feedback":
		case "review_changes_requested":
		case "agent_failed":
			return "attention";
		case "ready_to_merge":
		case "pr_opened":
			return "ready";
		case "pr_merged":
			return "merged";
		case "pr_closed_unmerged":
			return "closed";
		case "review_completed":
		case "agent_completed":
			return "done";
		default:
			return "info";
	}
}

/** The agent running a local session, from the workspace list already loaded. */
export function sessionProvider(queryClient: QueryClient, sessionId: string | undefined): AgentProvider | undefined {
	if (!sessionId) return undefined;
	for (const workspace of queryClient.getQueryData<WorkspaceSummary[]>(workspaceQueryKey) ?? []) {
		const session = workspace.sessions.find((candidate) => candidate.id === sessionId);
		if (session) return session.provider;
	}
	return undefined;
}
