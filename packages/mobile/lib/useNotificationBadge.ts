import { useCloudNotifications } from "./cloud/notifications";
import { useApp } from "./store";

export function useNotificationBadge() {
	const { hostStates, localConfigured } = useApp();
	const cloud = useCloudNotifications();
	return {
		available: cloud.available || localConfigured || hostStates.length > 0,
		unreadCount: hostStates.reduce((count, host) => count + host.notificationsUnread, 0) + cloud.unreadCount,
	};
}
