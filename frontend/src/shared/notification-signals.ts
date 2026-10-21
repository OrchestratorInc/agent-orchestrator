/**
 * Notification signal policy, shared by the main process (OS toast, sound,
 * dock/taskbar attention) and the renderer (bell and launcher badge counts).
 *
 * AO is quiet by default: only events that need human feedback ring, toast,
 * or badge. Everything else is recorded silently in the NotificationCenter
 * feed. The renderer forwards every `notification_created` event to
 * `notifications:show`, so the type reaching main is always one defined by
 * `backend/internal/domain/notification.go` (or its cloud control-plane
 * counterpart), and every signal decision routes through
 * {@link needsHumanFeedback} here rather than through string literals
 * scattered across call sites.
 */

/** The notification types defined by `backend/internal/domain/notification.go`. */
export type NotificationType =
	| "needs_input"
	| "ready_to_merge"
	| "pr_merged"
	| "pr_closed_unmerged"
	| "review_completed"
	| "review_changes_requested";

/**
 * The only event classes allowed to signal (bell, OS toast, sound, dock
 * bounce, unseen badges): an agent session blocked waiting on the user
 * (`needs_input` — the backend creates it from the session's domain
 * waiting-for-input state, not from text heuristics), a review verdict of
 * changes requested on the user's or curated PRs (`review_changes_requested`;
 * the cloud control plane reports the same verdict as `review_feedback`), and
 * a failed agent (`agent_failed`, cloud) that can only resume with the
 * owner's help. Deliberately quiet: done/checkpoint reports, `ready_to_merge`
 * and PR outcomes, spawns, panel activity, other lifecycle events. New types
 * default to quiet — extending this set is an explicit policy decision.
 */
const HUMAN_FEEDBACK_TYPES: ReadonlySet<string> = new Set<string>([
	"needs_input",
	"review_changes_requested",
	// Cloud control-plane types for the same signal classes.
	"review_feedback",
	"agent_failed",
]);

/** Whether this event class needs human feedback and may signal. */
export function needsHumanFeedback(type: string | undefined): boolean {
	return type !== undefined && HUMAN_FEEDBACK_TYPES.has(type);
}

/** Unread rows of the signal classes — the only ones the bell counts. */
export function signalUnreadCount(items: readonly { type?: string; status?: string }[]): number {
	return items.filter((item) => item.status === "unread" && needsHumanFeedback(item.type)).length;
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
 * Whether to fire an OS toast. Gated by the quiet-by-default policy: only
 * human-feedback types toast, so a routine notification never interrupts.
 */
export function shouldToast(notification: { title?: string; type?: string }, isSupported: boolean): boolean {
	return needsHumanFeedback(notification.type) && Boolean(notification.title) && isSupported;
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
