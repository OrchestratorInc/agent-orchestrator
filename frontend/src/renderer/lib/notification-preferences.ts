import type { HostId } from "./hosts";
import type { NotificationFilters } from "../../shared/ui-locale";

/**
 * The kinds Manage notifications offers, in the order it lists them. Each
 * groups the wire types that mean the same thing to the person, across the
 * three places the bell reads from:
 *
 * - The daemon, local or remote (`backend/internal/domain/notification.go`).
 * - AO Cloud agent events (needs input, completed, failed), its pull
 *   request and merge request rows, and its automation runs, whose `type`
 *   is a free string.
 *
 * A type no kind names falls under "other", so a kind added later still
 * notifies until the person turns "other" off.
 */
export const NOTIFICATION_KINDS = [
	{ id: "needs_input", types: ["needs_input"] },
	{ id: "agent_completed", types: ["agent_completed"] },
	{ id: "agent_failed", types: ["agent_failed"] },
	{ id: "pr_opened", types: ["pr_opened"] },
	{ id: "ci_failed", types: ["ci_failed"] },
	{ id: "review_completed", types: ["review_completed"] },
	{ id: "review_changes", types: ["review_changes_requested", "review_feedback"] },
	{ id: "ready_to_merge", types: ["ready_to_merge"] },
	{ id: "merge_requests", types: ["merge_requested", "merge_request_merged", "merge_request_rejected"] },
	{ id: "pr_merged", types: ["pr_merged"] },
	{ id: "pr_closed", types: ["pr_closed_unmerged"] },
	{ id: "automations", types: ["automation_run_completed", "automation_run_failed", "automation_run_skipped"] },
	{ id: "other", types: [] },
] as const satisfies readonly { id: string; types: readonly string[] }[];

export type NotificationKindId = (typeof NOTIFICATION_KINDS)[number]["id"];

const KIND_BY_TYPE = new Map<string, NotificationKindId>(
	NOTIFICATION_KINDS.flatMap((kind) => kind.types.map((type) => [type, kind.id] as const)),
);

export function notificationKind(type: string): NotificationKindId {
	return KIND_BY_TYPE.get(type) ?? "other";
}

/** Where a notification came from: this machine, AO Cloud, or a remote machine. */
export type NotificationSource = "local" | "cloud" | `remote:${string}`;

export function remoteNotificationSource(hostId: HostId): NotificationSource {
	return `remote:${hostId}`;
}

export type NotificationPreferences = {
	enabled: boolean;
	filters: NotificationFilters;
};

/** Whether a notification reaches the bell, its badge, and the OS. */
export function isNotificationAllowed(
	preferences: NotificationPreferences,
	type: string,
	source: NotificationSource,
): boolean {
	if (!preferences.enabled) return false;
	if (preferences.filters.mutedSources.includes(source)) return false;
	return !preferences.filters.mutedKinds.includes(notificationKind(type));
}

/** Whether every notification is allowed, so counts can come straight from the servers. */
export function allowsEveryNotification(preferences: NotificationPreferences): boolean {
	return (
		preferences.enabled &&
		preferences.filters.mutedKinds.length === 0 &&
		preferences.filters.mutedSources.length === 0
	);
}
