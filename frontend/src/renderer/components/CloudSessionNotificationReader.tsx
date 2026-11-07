import { useReadCloudSessionNotifications } from "../hooks/useCloudNotifications";
import { useUiStore } from "../stores/ui-store";

/**
 * Reads the notifications a cloud session raised for this person while it is
 * on screen; Settings covering it does not count. Its own component, so the
 * inbox and window focus it watches never re-render the session view.
 */
export function CloudSessionNotificationReader({ sessionId }: { sessionId: string }) {
	const settingsOpen = useUiStore((state) => state.settingsModal?.scope === "global");
	useReadCloudSessionNotifications(sessionId, !settingsOpen);
	return null;
}
