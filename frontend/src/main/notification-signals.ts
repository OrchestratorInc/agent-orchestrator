/**
 * Notification signal policy for the main process.
 *
 * The renderer forwards every `notification_created` event to `notifications:show`,
 * so the type reaching main is always one defined by
 * `backend/internal/domain/notification.go`. These helpers keep the decision of
 * "toast vs. active attention signal" in one typed, testable place rather than as
 * hardcoded string literals scattered through the IPC handler.
 */

/** The notification types defined by `backend/internal/domain/notification.go`. */
export type NotificationType = "needs_input" | "ready_to_merge" | "pr_merged" | "pr_closed_unmerged";

/**
 * Whether to fire the Windows/Linux taskbar flash. Restricted to the
 * actionable types: a merged or closed PR is worth a toast, but should not
 * demand attention as insistently as an agent blocked waiting on the user.
 *
 * The macOS dock bounce is deliberately NOT gated by this allowlist — every
 * notification bounces there, with urgency carried by {@link dockBounceType}.
 */
const ATTENTION_TYPES: ReadonlySet<string> = new Set<NotificationType>(["needs_input", "ready_to_merge"]);

/** Whether this notification type should flash the Windows/Linux taskbar. */
export function shouldSignalAttention(type: string | undefined): boolean {
	return type !== undefined && ATTENTION_TYPES.has(type);
}

/**
 * Whether the person is already looking at what a notification is about, so
 * its visual signals (toast, dock bounce, taskbar flash) are skipped: the
 * renderer says it is on screen (`watched`), or the window has focus. A
 * notification that asks to alert while focused (a cloud mention or aside
 * reply about a session that is not on screen) counts only `watched`, so it
 * alerts even while AO's window is focused on another session.
 */
export function alreadyLooking(
	windowFocused: boolean,
	notification: { watched?: boolean; alertWhenFocused?: boolean },
): boolean {
	if (notification.watched === true) return true;
	return windowFocused && notification.alertWhenFocused !== true;
}

/**
 * Whether a new macOS dock bounce should replace the pending one. A pending
 * "critical" bounce (agent blocked on the user) is never replaced: a later
 * informational notification must not downgrade it to a one-shot bounce, or
 * the blocked-agent signal would stop lasting until the user returns.
 */
export function shouldReplaceBounce(pending: { critical: boolean } | null): boolean {
	return pending === null || !pending.critical;
}

/**
 * Whether to fire an OS toast. Deliberately independent of the type list: every
 * backend notification type gets a toast, so adding a new type in
 * `notification.go` can never silently drop its toast (the bug this replaced).
 */
export function shouldToast(notification: { title?: string }, isSupported: boolean): boolean {
	return Boolean(notification.title) && isSupported;
}

/**
 * Platforms where Electron's Notification honours `silent`. The macOS
 * (cocoa_notification.mm) and Windows (windows_toast_notification.cc)
 * presenters read it; the Linux libnotify presenter never does and sends no
 * freedesktop `suppress-sound` hint, so on Linux the daemon alone decides
 * whether a toast chimes (Electron 33.4.11). The "Sound notifications"
 * preference therefore promises control over AO-owned audio everywhere, and
 * over the OS chime only where this returns true.
 */
export function osToastChimeControllable(platform: NodeJS.Platform): boolean {
	return platform === "darwin" || platform === "win32";
}

/**
 * The `silent` value for the OS toast, or `undefined` where the platform
 * ignores it (see osToastChimeControllable) so the call site cannot read a
 * muted toast into a value the presenter drops on the floor.
 *
 * Where it is honoured, two reasons mute: the user turned sound notifications
 * off (the preference has to silence every chime, not just ours), or AO is
 * about to play its own sound and must not layer the system chime on top of
 * it. Otherwise (sound on, informational type) the toast keeps its native
 * chime, which is the only sound that type gets.
 */
export function toastSilent(
	platform: NodeJS.Platform,
	soundNotificationsEnabled: boolean,
	playsSound: boolean,
): boolean | undefined {
	if (!osToastChimeControllable(platform)) return undefined;
	return !soundNotificationsEnabled || playsSound;
}

/**
 * macOS dock bounce style. A blocked agent waiting on the user keeps bouncing
 * until the app is activated ("critical"); anything else bounces once
 * ("informational"). This is where urgency lives, so every notification can
 * signal without a merged PR nagging as insistently as a blocked agent.
 */
export function dockBounceType(type: string | undefined): "critical" | "informational" {
	return type === "needs_input" ? "critical" : "informational";
}

/**
 * The OS toasts showing now, one per tag. A toast with a tag replaces the one
 * already showing under it (a cloud mention tags by session, so a repeated
 * mention in a session replaces its alert instead of stacking); untagged
 * toasts stack as they always have. Toasts forget themselves when closed, and
 * the oldest tag is dropped past `max` so the map cannot grow without bound.
 */
export function createTaggedToasts<Toast extends { close(): void }>(max = 64) {
	const byTag = new Map<string, Toast>();
	return {
		show(tag: string | undefined, toast: Toast): void {
			if (!tag) return;
			const previous = byTag.get(tag);
			byTag.delete(tag);
			if (previous && previous !== toast) previous.close();
			byTag.set(tag, toast);
			if (byTag.size > max) byTag.delete(byTag.keys().next().value!);
		},
		forget(tag: string | undefined, toast: Toast): void {
			if (tag && byTag.get(tag) === toast) byTag.delete(tag);
		},
		get size(): number {
			return byTag.size;
		},
	};
}
