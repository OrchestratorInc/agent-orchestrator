import { describe, expect, it } from "vitest";
import type { AgentAccount } from "../hooks/useAgentAccounts";
import { formatCountdown, groupAccounts, limitWindowPace, remainingPercent, spanPace, type UsageLimitSpan } from "./usage-limits";

function account(overrides: Partial<AgentAccount>): AgentAccount {
	return { id: "a", provider: "codex", default: false, device: false, signedIn: true, ...overrides };
}

describe("groupAccounts", () => {
	it("lists every account under its provider, default first", () => {
		const grouped = groupAccounts([
			account({ id: "claude-b", provider: "claude", limits: { fetchedAt: "2026-10-25T10:07:00Z", windows: [] } }),
			account({ id: "claude-a", provider: "claude", default: true, limits: { fetchedAt: "2026-10-25T10:00:00Z", windows: [{ id: "five_hour", label: "Session (5h)", usedPercent: 20, resetsAt: "2026-10-25T13:00:00Z" }] } }),
			account({ id: "codex-a", default: true, signedIn: false }),
		]);
		expect(grouped.map((entry) => entry.provider)).toEqual(["codex", "claude"]);
		expect(grouped[0]).toEqual({ provider: "codex", accounts: [expect.objectContaining({ id: "codex-a" })], checkedAt: null });
		expect(grouped[1].accounts.map((entry) => entry.id)).toEqual(["claude-a", "claude-b"]);
		expect(grouped[1].checkedAt).toBe("2026-10-25T10:07:00Z");
	});

	it("skips providers without accounts", () => {
		expect(groupAccounts([])).toEqual([]);
	});
});

describe("remainingPercent", () => {
	it("is what is left of the window, clamped to 0-100", () => {
		expect(remainingPercent(18)).toBe(82);
		expect(remainingPercent(130)).toBe(0);
		expect(remainingPercent(-5)).toBe(100);
	});
});

describe("formatCountdown", () => {
	const now = new Date("2026-10-25T10:00:00Z");
	it("uses the two largest units", () => {
		expect(formatCountdown("2026-10-25T13:45:00Z", now)).toBe("3h 45m");
		expect(formatCountdown("2026-10-31T10:00:00Z", now)).toBe("6d 0h");
		expect(formatCountdown("2026-10-25T10:11:30Z", now)).toBe("12m");
	});
	it("returns nothing for a passed or missing reset", () => {
		expect(formatCountdown("2026-10-25T09:00:00Z", now)).toBeNull();
		expect(formatCountdown(null, now)).toBeNull();
	});
});

describe("spanPace", () => {
	const span = (overrides: Partial<UsageLimitSpan>): UsageLimitSpan => ({
		windowId: "codex:Weekly", label: "Weekly", durationMinutes: 10080, state: "open", readings: 2,
		startsAt: "2026-10-28T06:35:00Z", resetsAt: "2026-11-04T06:35:00Z",
		firstReadAt: "2026-10-28T07:00:00Z", lastReadAt: "2026-10-29T06:35:00Z", peakUsedPercent: 20, ...overrides,
	});

	it("projects the pace since the window opened to the reset", () => {
		// 10% in the first of seven days lands at 70%; 25% in it runs out three days later.
		const pace = spanPace(span({ peakUsedPercent: 10 }));
		expect(pace?.atReset).toBeCloseTo(70);
		expect(pace?.runsOutAt).toBeNull();
		const fast = spanPace(span({ points: [{ at: "2026-10-29T06:35:00Z", usedPercent: 25 }] }));
		expect(fast?.atReset).toBe(100);
		expect(Math.abs((fast?.runsOutAt?.getTime() ?? 0) - Date.parse("2026-11-01T06:35:00Z"))).toBeLessThan(1000);
	});

	it("says nothing in the first hour or without a start", () => {
		expect(spanPace(span({ lastReadAt: "2026-10-28T07:00:00Z" }))).toBeNull();
		expect(spanPace(span({ startsAt: null }))).toBeNull();
	});
});

describe("limitWindowPace", () => {
	const window = { id: "five_hour", label: "Session (5h)", durationMinutes: 300, resetsAt: "2026-10-30T12:00:00Z", usedPercent: 30 };

	it("projects a live reading from when its window opened", () => {
		// Opened at 07:00; 30% by 09:00 is 15% an hour, so about 75% by the reset.
		const pace = limitWindowPace(window, "2026-10-30T09:00:00Z");
		expect(pace?.atReset).toBeCloseTo(75);
		expect(pace?.runsOutAt).toBeNull();
		// 50% by 09:00 reaches 100% two hours later, before the reset.
		const runsOut = limitWindowPace({ ...window, usedPercent: 50 }, "2026-10-30T09:00:00Z")?.runsOutAt?.getTime() ?? 0;
		expect(Math.abs(runsOut - Date.parse("2026-10-30T11:00:00Z"))).toBeLessThan(1000);
	});

	it("says nothing without a length, a reset, any use, at the limit, or before half an hour", () => {
		expect(limitWindowPace({ ...window, durationMinutes: undefined }, "2026-10-30T09:00:00Z")).toBeNull();
		expect(limitWindowPace({ ...window, resetsAt: null }, "2026-10-30T09:00:00Z")).toBeNull();
		expect(limitWindowPace({ ...window, usedPercent: 0 }, "2026-10-30T09:00:00Z")).toBeNull();
		expect(limitWindowPace({ ...window, usedPercent: 100 }, "2026-10-30T09:00:00Z")).toBeNull();
		expect(limitWindowPace(window, "2026-10-30T07:20:00Z")).toBeNull();
	});
});
