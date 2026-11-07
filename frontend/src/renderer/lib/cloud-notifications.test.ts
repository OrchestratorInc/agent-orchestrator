import { describe, expect, it } from "vitest";
import { CloudCpError } from "./cloud-cp/errors";
import type { CloudCpNotification, CloudCpNotificationEvent, CloudCpNotificationListResponse } from "./cloud-cp/types";
import {
	applyCloudInboxChange,
	cloudAlertKey,
	cloudInboxUnavailable,
	cloudNotificationsQueryKey,
	cloudSnoozeUntil,
	isLegacyCloudNeedsYou,
	shouldAlertCloudNotification,
} from "./cloud-notifications";

describe("cloudNotificationsQueryKey", () => {
	it("normalizes the base URL so a trailing slash yields the same key", () => {
		expect(cloudNotificationsQueryKey("https://cp.example/", "org-1", "user-1", "all")).toEqual(
			cloudNotificationsQueryKey("https://cp.example", "org-1", "user-1", "all"),
		);
	});

	it("strips repeated trailing slashes and keeps status distinct", () => {
		expect(cloudNotificationsQueryKey("https://cp.example///", "org-1", "user-1", "unread")).toEqual([
			"cloud-notifications",
			"https://cp.example",
			"org-1",
			"user-1",
			"unread",
		]);
	});

	it("keeps notifications from separate signed-in users in separate caches", () => {
		expect(cloudNotificationsQueryKey("https://cp.example", "org-1", "user-1")).not.toEqual(
			cloudNotificationsQueryKey("https://cp.example", "org-1", "user-2"),
		);
	});
});

describe("team notification helpers", () => {
	const row = (overrides: Partial<CloudCpNotification> = {}): CloudCpNotification => ({
		id: "n1", source: "cloud", orgId: "org-1", type: "approval_needed", title: "Fix auth needs approval", body: "",
		status: "unread", createdAt: "2026-11-07T10:00:00Z", updatedAt: "2026-11-07T10:00:00Z", eventId: "approval:r1",
		tier: 0, desktop: true, ...overrides,
	});
	const event = (notification: CloudCpNotification, kind: CloudCpNotificationEvent["kind"] = "notification_created"): CloudCpNotificationEvent => ({
		sequence: 1, orgId: "org-1", recipientUserId: "u1", kind, notification, createdAt: notification.createdAt,
	});
	const now = Date.parse("2026-11-07T12:00:00Z");

	it("alerts the desktop only for an unread row the person wants there that still waits on them", () => {
		expect(shouldAlertCloudNotification(event(row()), now)).toBe(true);
		expect(shouldAlertCloudNotification(event(row(), "notification_updated"), now)).toBe(true);
		expect(shouldAlertCloudNotification(event(row(), "notification_resolved"), now)).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ desktop: false })), now)).toBe(false);
		// Rows from control planes before team notifications carry no desktop flag.
		expect(shouldAlertCloudNotification(event(row({ desktop: undefined })), now)).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ status: "read" })), now)).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ resolvedAt: "2026-11-07T11:00:00Z" })), now)).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ doneAt: "2026-11-07T11:00:00Z" })), now)).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ snoozedUntil: "2026-11-07T13:00:00Z" })), now)).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ snoozedUntil: "2026-11-07T11:00:00Z" })), now)).toBe(true);
	});

	it("keys an alert by the row and what raised it, not by its read state", () => {
		expect(cloudAlertKey(row())).toBe(cloudAlertKey(row({ status: "read", updatedAt: "2026-11-07T11:00:00Z" })));
		expect(cloudAlertKey(row())).not.toBe(cloudAlertKey(row({ eventId: "approval:r2" })));
	});

	it("snoozes for an hour or until nine tomorrow morning, local time", () => {
		const from = new Date(2026, 10, 7, 22, 30);
		expect(cloudSnoozeUntil("hour", from)).toEqual(new Date(2026, 10, 7, 23, 30));
		expect(cloudSnoozeUntil("tomorrow", from)).toEqual(new Date(2026, 10, 8, 9, 0));
		expect(cloudSnoozeUntil("tomorrow", new Date(2026, 11, 31, 8, 0))).toEqual(new Date(2027, 0, 1, 9, 0));
	});

	it("applies a change to a cached page the way the server will", () => {
		const page: CloudCpNotificationListResponse = {
			items: [row(), row({ id: "n2", tier: 2, status: "read" })], page: { hasMore: false }, unreadCount: 4, latestSequence: 9, needsYouCount: 2,
		};
		const done = applyCloudInboxChange(page, { ids: ["n1"], done: true });
		expect(done.items.map((item) => item.id)).toEqual(["n2"]);
		expect(done.needsYouCount).toBe(1);
		expect(done.unreadCount).toBe(4);
		const snoozed = applyCloudInboxChange(page, { ids: ["n1"], snoozedUntil: "2026-11-08T09:00:00Z" });
		expect(snoozed.items.map((item) => item.id)).toEqual(["n2"]);
		const read = applyCloudInboxChange(page, { ids: ["n1", "n2"], read: true });
		expect(read.items.map((item) => item.status)).toEqual(["read", "read"]);
		expect(read.unreadCount).toBe(3);
		expect(read.needsYouCount).toBe(2);
		const unsnoozed = applyCloudInboxChange(page, { ids: ["n1"], snoozedUntil: "" });
		expect(unsnoozed.items).toHaveLength(2);
	});

	it("knows a control plane without the inbox from its missing route", () => {
		expect(cloudInboxUnavailable(new CloudCpError("404 page not found", { status: 404 }))).toBe(true);
		expect(cloudInboxUnavailable(new CloudCpError("Not available", { status: 501, code: "not_available" }))).toBe(true);
		expect(cloudInboxUnavailable(new CloudCpError("Session not found", { status: 404, code: "not_found" }))).toBe(false);
		expect(cloudInboxUnavailable(new CloudCpError("Down", { status: 503 }))).toBe(false);
		expect(cloudInboxUnavailable(new Error("offline"))).toBe(false);
	});

	it("puts a legacy row in Needs you only while its question is unanswered", () => {
		expect(isLegacyCloudNeedsYou(row({ tier: undefined, type: "needs_input" }))).toBe(true);
		expect(isLegacyCloudNeedsYou(row({ tier: undefined, type: "needs_input", resolvedAt: "2026-11-07T11:00:00Z" }))).toBe(false);
		expect(isLegacyCloudNeedsYou(row({ tier: undefined, type: "pr_merged" }))).toBe(false);
		expect(isLegacyCloudNeedsYou(row({ tier: 0 }))).toBe(true);
		expect(isLegacyCloudNeedsYou(row({ tier: 2, type: "needs_input" }))).toBe(false);
	});
});
