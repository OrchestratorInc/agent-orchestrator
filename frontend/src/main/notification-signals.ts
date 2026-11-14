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

/** The part of an Electron Notification the registry needs. */
export interface DismissibleToast {
	close(): void;
}

/**
 * Retains shown OS toasts by their exact notification id so AO can withdraw one
 * when the issue behind it is resolved. Without a reference the toast cannot be
 * closed (and Electron may collect it, dropping its click handler).
 *
 * Ids are the exact strings the renderer passed to `notifications:show`: a bare
 * daemon id for this machine, a host-qualified id for a remote one, so two
 * machines reusing a daemon id never dismiss each other's toast.
 *
 * Dismissing only withdraws the OS banner. It is not acknowledgement: the
 * stored notification's read state is untouched.
 */
export function createToastRegistry<T extends DismissibleToast>() {
	const toasts = new Map<string, T>();
	return {
		/** Tracks a newly shown toast, closing an older one shown for the same id. */
		retain(id: string, toast: T): void {
			const previous = toasts.get(id);
			toasts.delete(id);
			if (previous && previous !== toast) previous.close();
			// No eviction: forgetting a live toast would make its later resolution
			// unable to withdraw it. Entries leave only through release (the OS or
			// the user closed it) or dismiss (AO resolved it).
			toasts.set(id, toast);
		},
		/** Forgets a toast the OS or the user already closed, if it is still the current one. */
		release(id: string, toast: T): void {
			if (toasts.get(id) === toast) toasts.delete(id);
		},
		/** Closes the toast shown for exactly this id. Unknown ids are a no-op. */
		dismiss(id: string): boolean {
			const toast = toasts.get(id);
			if (!toast) return false;
			toasts.delete(id);
			toast.close();
			return true;
		},
		get size(): number {
			return toasts.size;
		},
	};
}
