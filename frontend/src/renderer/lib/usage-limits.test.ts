import { describe, expect, it } from "vitest";
import type { AgentAccount } from "../hooks/useAgentAccounts";
import { formatCountdown, poolLimits } from "./usage-limits";

function account(overrides: Partial<AgentAccount>): AgentAccount {
	return { id: "a", provider: "codex", default: false, device: false, signedIn: true, ...overrides };
}

describe("poolLimits", () => {
	it("averages each window across accounts and keeps one column per account", () => {
		const pooled = poolLimits([
			account({ id: "claude-a", provider: "claude", default: true, limits: { fetchedAt: "2026-10-25T10:00:00Z", windows: [{ id: "five_hour", label: "Session (5h)", usedPercent: 20, resetsAt: "2026-10-25T13:00:00Z" }] } }),
			account({ id: "codex-b", limits: { fetchedAt: "2026-10-25T10:05:00Z", resetCredits: 1, windows: [{ id: "codex:Weekly", label: "Weekly", usedPercent: 80, resetsAt: "2026-10-30T00:00:00Z" }] } }),
			account({ id: "codex-a", default: true, limits: { fetchedAt: "2026-10-25T09:00:00Z", resetCredits: 2, windows: [
				{ id: "codex:5h", label: "5h", usedPercent: 10, resetsAt: "2026-10-25T12:00:00Z" },
				{ id: "codex:Weekly", label: "Weekly", usedPercent: 40, resetsAt: "2026-10-28T00:00:00Z" },
			] } }),
		]);
		expect(pooled.map((entry) => entry.provider)).toEqual(["codex", "claude"]);
		const codex = pooled[0];
		expect(codex.accounts.map((entry) => entry.id)).toEqual(["codex-a", "codex-b"]);
		expect(codex.resetCredits).toBe(3);
		expect(codex.checkedAt).toBe("2026-10-25T10:05:00Z");
		expect(codex.windows.map((window) => window.id)).toEqual(["codex:5h", "codex:Weekly"]);
		const weekly = codex.windows[1];
		expect(weekly.remainingPercent).toBe(40);
		expect(weekly.segments.map((segment) => segment.remainingPercent)).toEqual([60, 20]);
		expect(weekly.nextResetAt).toBe("2026-10-28T00:00:00Z");
		// codex-b reports no 5h window: its column stays, empty.
		expect(codex.windows[0].segments).toEqual([
			{ accountId: "codex-a", remainingPercent: 90, resetsAt: "2026-10-25T12:00:00Z" },
			{ accountId: "codex-b", remainingPercent: null, resetsAt: null },
		]);
		expect(codex.windows[0].remainingPercent).toBe(90);
	});

	it("skips providers without accounts", () => {
		expect(poolLimits([])).toEqual([]);
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
