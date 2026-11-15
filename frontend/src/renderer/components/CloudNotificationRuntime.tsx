import { useCloudNotifications } from "../hooks/useCloudNotifications";

export function CloudNotificationRuntime() {
	useCloudNotifications("all");
	return null;
}
