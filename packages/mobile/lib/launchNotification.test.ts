import { beforeEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ items: new Map<string, string>(), last: null as unknown }));

vi.mock("@react-native-async-storage/async-storage", () => ({ default: {
	getItem: async (key: string) => mocks.items.get(key) ?? null,
	setItem: async (key: string, value: string) => { mocks.items.set(key, value); },
} }));
vi.mock("expo-notifications", () => ({ getLastNotificationResponseAsync: async () => mocks.last }));

import type * as Notifications from "expo-notifications";
import { launchNotificationResponse, markNotificationResponseHandled, resetLaunchNotificationResponse } from "./launchNotification";

const tap = (identifier: string, date: number) => ({ notification: { request: { identifier }, date }, actionIdentifier: "expo.modules.notifications.actions.DEFAULT" }) as unknown as Notifications.NotificationResponse;

beforeEach(() => {
	mocks.items.clear();
	mocks.last = null;
	resetLaunchNotificationResponse();
});

describe("the notification tap that launched the app", () => {
	it("is the system's last tap the first time, and the same answer for every caller", async () => {
		mocks.last = tap("n1", 100);
		const first = launchNotificationResponse();
		const second = launchNotificationResponse();
		expect(await first).toBe(mocks.last);
		expect(await second).toBe(mocks.last);
	});

	// The system keeps the last tap across launches: once handled, a later
	// launch from the app icon must not route it again (or skip reopening the
	// last screen for it).
	it("is none on a later launch that still finds the same tap", async () => {
		mocks.last = tap("n1", 100);
		await launchNotificationResponse();
		resetLaunchNotificationResponse();
		expect(await launchNotificationResponse()).toBeNull();
	});

	it("is none for a tap the running app already routed", async () => {
		mocks.last = tap("n2", 200);
		await markNotificationResponseHandled(tap("n2", 200));
		expect(await launchNotificationResponse()).toBeNull();
	});

	it("is a new tap after an older one was handled", async () => {
		await markNotificationResponseHandled(tap("n1", 100));
		mocks.last = tap("n3", 300);
		expect(await launchNotificationResponse()).toBe(mocks.last);
	});
});
