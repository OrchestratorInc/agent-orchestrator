import { describe, expect, it } from "vitest";
import type { AgentAccount } from "../hooks/useAgentAccounts";
import { formatCountdown, groupAccounts, remainingPercent } from "./usage-limits";

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
