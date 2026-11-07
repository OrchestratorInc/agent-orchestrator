import { useCloudNotifications } from "../hooks/useCloudNotifications";

// Kept beside the local runtime in the shell. This deliberately has no local
// daemon subscriptions: its hook is gated by cloud auth and owns only
// cloud-qualified cache entries. It holds the one live stream that keeps the
// cloud inbox current and feeds desktop notifications (NotificationRuntime).
export function CloudNotificationRuntime() {
	useCloudNotifications({ live: true });
	return null;
}
