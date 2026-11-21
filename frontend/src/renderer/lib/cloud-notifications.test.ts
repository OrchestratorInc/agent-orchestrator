import { beforeEach, describe, expect, it } from "vitest";
import { CloudCpError } from "./cloud-cp/errors";
import type { CloudCpNotification, CloudCpNotificationEvent } from "./cloud-cp/types";
import {
	cloudAlertKey,
	cloudInboxQueryKey,
	cloudInboxSupport,
	cloudInboxUnavailable,
	cloudNotificationsQueryKey,
	mentionMetadata,
	probeCloudInbox,
	resetCloudInboxSupport,
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

describe("the /me inbox key", () => {
	it("never meets an organization's key", () => {
		expect(cloudInboxQueryKey("https://cp.example/", "user-1")).toEqual(["cloud-notifications", "https://cp.example", "me", "user-1", "all"]);
	});
});

describe("whether a control plane has the /me inbox", () => {
	beforeEach(() => {
		window.localStorage.clear();
		resetCloudInboxSupport();
	});

	it("reads a bare 404, a 405 or a 501 as a missing route, and nothing else", () => {
		expect(cloudInboxUnavailable(new CloudCpError("x", { status: 404 }))).toBe(true);
		expect(cloudInboxUnavailable(new CloudCpError("x", { status: 405 }))).toBe(true);
		expect(cloudInboxUnavailable(new CloudCpError("x", { status: 501 }))).toBe(true);
		expect(cloudInboxUnavailable(new CloudCpError("x", { status: 404, code: "not_found" }))).toBe(false);
		expect(cloudInboxUnavailable(new CloudCpError("x", { status: 503 }))).toBe(false);
	});

	it("learns it from any answer and remembers it on this device, so the next run draws at once", async () => {
		expect(cloudInboxSupport("https://cp.example")).toBe("unknown");
		await probeCloudInbox("https://cp.example/", async () => ({}));
		expect(cloudInboxSupport("https://cp.example")).toBe("supported");
		expect(window.localStorage.getItem("ao.cloudInboxSupport:https://cp.example")).toBe("supported");
	});

	it("learns a missing route, and keeps an outage undecided", async () => {
		await expect(probeCloudInbox("https://old.example", () => Promise.reject(new CloudCpError("x", { status: 503 })))).rejects.toThrow();
		expect(cloudInboxSupport("https://old.example")).toBe("unknown");
		await expect(probeCloudInbox("https://old.example", () => Promise.reject(new CloudCpError("x", { status: 404 })))).rejects.toThrow();
		expect(cloudInboxSupport("https://old.example")).toBe("unsupported");
	});
});

describe("mention alerts", () => {
	const row = (overrides: Partial<CloudCpNotification> = {}): CloudCpNotification => ({
		id: "n-1",
		source: "cloud",
		orgId: "org-1",
		sessionId: "s-1",
		type: "mentioned",
		title: "Chen Li mentioned you",
		body: "Can you check the retry backoff?",
		status: "unread",
		metadata: { authorUserId: "u-chen", authorName: "Chen Li", sequence: 42 },
		createdAt: "2026-11-13T10:00:00Z",
		updatedAt: "2026-11-13T10:00:00Z",
		...overrides,
	});
	const event = (notification: CloudCpNotification, kind: CloudCpNotificationEvent["kind"] = "notification_created"): CloudCpNotificationEvent => ({
		sequence: 1, orgId: "org-1", recipientUserId: "u-me", kind, notification, createdAt: notification.createdAt,
	});
	const team = row({ title: "Chen Li mentioned Platform", metadata: { authorName: "Chen Li", sequence: 42, teamId: "t-1", teamName: "Platform" } });

	it("alerts for a new or repeated mention, by default", () => {
		expect(shouldAlertCloudNotification(event(row()))).toBe(true);
		expect(shouldAlertCloudNotification(event(team))).toBe(true);
	});

	it("follows the two switches: mentions of me, and of a team I'm in", () => {
		expect(shouldAlertCloudNotification(event(row()), { mentions: false, teamMentions: true })).toBe(false);
		expect(shouldAlertCloudNotification(event(team), { mentions: false, teamMentions: true })).toBe(true);
		expect(shouldAlertCloudNotification(event(team), { mentions: true, teamMentions: false })).toBe(false);
		expect(shouldAlertCloudNotification(event(row()), { mentions: true, teamMentions: false })).toBe(true);
	});

	it("never alerts for a read, resolved or updated row, or another kind", () => {
		expect(shouldAlertCloudNotification(event(row({ status: "read" })))).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ resolvedAt: "2026-11-13T10:01:00Z" })))).toBe(false);
		expect(shouldAlertCloudNotification(event(row(), "notification_updated"))).toBe(false);
		expect(shouldAlertCloudNotification(event(row({ type: "pr_merged" })))).toBe(false);
	});

	it("alerts once per thing that raised the row: a newer mention in the same session alerts again", () => {
		expect(cloudAlertKey(row({ eventId: "e-1" }))).not.toBe(cloudAlertKey(row({ eventId: "e-2" })));
		expect(cloudAlertKey(row({ eventId: "e-1" }))).toBe(cloudAlertKey(row({ eventId: "e-1", status: "read" })));
	});

	it("reads a mention's metadata defensively", () => {
		expect(mentionMetadata(row({ metadata: { authorName: 7, sequence: "42", teamId: "t-1" } }))).toEqual({
			authorUserId: undefined, authorName: undefined, sequence: undefined, turnId: undefined,
			teamId: "t-1", teamName: undefined, sessionTitle: undefined, projectName: undefined,
		});
		expect(mentionMetadata(row({ type: "pr_merged" }))).toBeUndefined();
	});
});
