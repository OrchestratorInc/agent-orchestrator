import { describe, expect, it } from "vitest";
import {
	NOTIFICATION_KINDS,
	allowsEveryNotification,
	isNotificationAllowed,
	notificationKind,
	remoteNotificationSource,
	type NotificationPreferences,
} from "./notification-preferences";

const everything: NotificationPreferences = { enabled: true, filters: { mutedKinds: [], mutedSources: [] } };

describe("notification preferences", () => {
	it("files every type the daemon and AO Cloud send under one kind", () => {
		expect(
			Object.fromEntries(
				[
					"needs_input",
					"ready_to_merge",
					"pr_merged",
					"pr_closed_unmerged",
					"review_completed",
					"review_changes_requested",
					"review_feedback",
					"agent_completed",
					"agent_failed",
					"pr_opened",
					"ci_failed",
					"merge_requested",
					"merge_request_merged",
					"merge_request_rejected",
				].map((type) => [type, notificationKind(type)]),
			),
		).toEqual({
			needs_input: "needs_input",
			ready_to_merge: "ready_to_merge",
			pr_merged: "pr_merged",
			pr_closed_unmerged: "pr_closed",
			review_completed: "review_completed",
			review_changes_requested: "review_changes",
			review_feedback: "review_changes",
			agent_completed: "agent_completed",
			agent_failed: "agent_failed",
			pr_opened: "pr_opened",
			ci_failed: "ci_failed",
			merge_requested: "merge_requests",
			merge_request_merged: "merge_requests",
			merge_request_rejected: "merge_requests",
		});
	});

	it("files a type nobody named yet under other", () => {
		expect(notificationKind("aside_reply")).toBe("other");
		expect(NOTIFICATION_KINDS.at(-1)?.id).toBe("other");
	});

	it("allows everything until something is turned off", () => {
		expect(allowsEveryNotification(everything)).toBe(true);
		expect(isNotificationAllowed(everything, "needs_input", "local")).toBe(true);
		expect(isNotificationAllowed(everything, "brand_new_type", remoteNotificationSource("host-a"))).toBe(true);
	});

	it("ignores muted kinds and machines, and everything while notifications are off", () => {
		const preferences: NotificationPreferences = {
			enabled: true,
			filters: { mutedKinds: ["pr_merged", "other"], mutedSources: ["remote:host-a"] },
		};
		expect(allowsEveryNotification(preferences)).toBe(false);
		expect(isNotificationAllowed(preferences, "pr_merged", "cloud")).toBe(false);
		expect(isNotificationAllowed(preferences, "brand_new_type", "cloud")).toBe(false);
		expect(isNotificationAllowed(preferences, "needs_input", "remote:host-a")).toBe(false);
		expect(isNotificationAllowed(preferences, "needs_input", "remote:host-b")).toBe(true);
		expect(isNotificationAllowed({ ...everything, enabled: false }, "needs_input", "local")).toBe(false);
		expect(allowsEveryNotification({ ...everything, enabled: false })).toBe(false);
	});
});
