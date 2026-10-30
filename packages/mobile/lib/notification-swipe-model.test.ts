import { describe, expect, it } from "vitest";

import {
	boundNotificationSwipeTranslation,
	shouldClearNotificationSwipe,
} from "./notification-swipe-model";

describe("notification swipe", () => {
	it("only follows the finger toward the destructive left edge", () => {
		expect(boundNotificationSwipeTranslation(24, 320)).toBe(0);
		expect(boundNotificationSwipeTranslation(-140, 320)).toBe(-140);
		expect(boundNotificationSwipeTranslation(-400, 320)).toBe(-320);
	});

	it("requires a near-full left swipe before clearing", () => {
		expect(shouldClearNotificationSwipe(-180, 320)).toBe(false);
		expect(shouldClearNotificationSwipe(-230.4, 320)).toBe(true);
		expect(shouldClearNotificationSwipe(-300, 0)).toBe(false);
	});
});
