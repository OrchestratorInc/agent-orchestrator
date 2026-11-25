import AsyncStorage from "@react-native-async-storage/async-storage";
import * as Notifications from "expo-notifications";

// The notification tap that launched the app, if one did, read once and shared
// by PushManager (which routes it) and RouteRestorer (which then does not
// reopen the last screen over it).
//
// getLastNotificationResponseAsync answers the last tap the system kept, which
// can be one already handled: a tap from an earlier launch, or one the running
// app routed through its listener. Each handled tap is marked by its identity,
// so one already marked never counts as the tap that launched this launch.

export const HANDLED_NOTIFICATION_KEY = "ao.notification.handled.v1";

export function notificationResponseKey(response: Notifications.NotificationResponse): string {
	return JSON.stringify([response.notification.request.identifier, response.actionIdentifier, response.notification.date]);
}

/** Marks a tap as handled, so a later launch does not take it for its own. */
export async function markNotificationResponseHandled(response: Notifications.NotificationResponse): Promise<void> {
	await AsyncStorage.setItem(HANDLED_NOTIFICATION_KEY, notificationResponseKey(response)).catch(() => {});
}

let launch: Promise<Notifications.NotificationResponse | null> | null = null;

/** The tap that launched the app, or null; the same answer for every caller. */
export function launchNotificationResponse(): Promise<Notifications.NotificationResponse | null> {
	launch ??= (async () => {
		const response = await Notifications.getLastNotificationResponseAsync().catch(() => null);
		if (!response) return null;
		const handled = await AsyncStorage.getItem(HANDLED_NOTIFICATION_KEY).catch(() => null);
		if (handled === notificationResponseKey(response)) return null;
		await markNotificationResponseHandled(response);
		return response;
	})();
	return launch;
}

/** Tests only: forgets the launch's answer, as a new launch would. */
export function resetLaunchNotificationResponse(): void {
	launch = null;
}
