import { afterEach, describe, expect, it, vi } from "vitest";
import { refetchWhenOlderThan } from "./refetch-when-older-than";

describe("refetchWhenOlderThan", () => {
	afterEach(() => vi.useRealTimers());

	it("refetches once the data is at least the given age, and not before", () => {
		vi.useFakeTimers();
		vi.setSystemTime(100_000);
		const decide = refetchWhenOlderThan(10_000);
		expect(decide({ state: { dataUpdatedAt: 95_000 } })).toBe(false);
		expect(decide({ state: { dataUpdatedAt: 90_000 } })).toBe("always");
		expect(decide({ state: { dataUpdatedAt: 0 } })).toBe("always");
	});
});
