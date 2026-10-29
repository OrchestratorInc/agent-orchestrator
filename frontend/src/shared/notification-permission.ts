/**
 * What a test notification revealed about desktop notification delivery. The
 * OS offers no permission query to Electron, so the only evidence is whether
 * a real toast showed ("granted") or the presenter reported a failure
 * ("denied", which on macOS and Windows means notifications are turned off for
 * the app). "unknown" means neither happened in time (for example, the macOS
 * permission prompt is still open); "unsupported" means the platform has no
 * notification presenter at all.
 */
export const NOTIFICATION_PERMISSION_STATES = ["granted", "denied", "unsupported", "unknown"] as const;
export type NotificationPermissionState = (typeof NOTIFICATION_PERMISSION_STATES)[number];

/** Copy for the test toast, localized by the renderer. */
export type TestNotificationCopy = { title: string; body: string };

/** The packaged app's bundle id (forge.config.ts appBundleId). */
const MAC_BUNDLE_ID = "dev.agent-orchestrator.desktop";

/** System notification settings for this app, or null where the OS has no deep link. */
export function notificationSettingsUrl(platform: string): string | null {
	if (platform === "darwin") return `x-apple.systempreferences:com.apple.Notifications-Settings.extension?id=${MAC_BUNDLE_ID}`;
	if (platform === "win32") return "ms-settings:notifications";
	return null;
}
