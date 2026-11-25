import { render, screen } from "@testing-library/react";
import { expect, it } from "vitest";
import type { UsageLimitLogin, UsageLimitSpan, UsageLimitWindows } from "../../lib/usage-limits";
import { UsageWindows } from "./UsageWindows";

it("keeps weekly cards mounted when same-email logins reorder or disappear", () => {
	const now = new Date("2026-11-25T12:00:00Z");
	const weeklyWindow: UsageLimitSpan = {
		windowId: "seven_day",
		label: "Weekly",
		durationMinutes: 10080,
		resetsAt: "2026-11-26T12:00:00Z",
		firstReadAt: "2026-11-25T10:00:00Z",
		lastReadAt: "2026-11-25T11:30:00Z",
		peakUsedPercent: 25,
		readings: 1,
		state: "open",
	};
	const team: UsageLimitLogin = {
		loginId: "team-history",
		accountId: "device-claude",
		provider: "claude",
		email: "same@example.com",
		plan: "team",
		windows: [weeklyWindow],
	};
	const personal: UsageLimitLogin = {
		...team,
		loginId: "personal-history",
		plan: "max",
		windows: [{ ...weeklyWindow, peakUsedPercent: 90 }],
	};
	const history: UsageLimitWindows = { since: "2026-11-18T12:00:00Z", until: now.toISOString(), logins: [personal, team] };
	const { rerender } = render(<UsageWindows history={history} now={now} />);
	const teamChart = screen.getByRole("img", { name: "Weekly: 25%" });
	const personalChart = screen.getByRole("img", { name: "Weekly: 90%" });

	rerender(<UsageWindows history={{ ...history, logins: [team, personal] }} now={now} />);
	expect(screen.getByRole("img", { name: "Weekly: 25%" })).toBe(teamChart);
	expect(screen.getByRole("img", { name: "Weekly: 90%" })).toBe(personalChart);

	rerender(<UsageWindows history={{ ...history, logins: [{ ...team, accountId: "saved-team" }] }} now={now} />);
	expect(screen.getByRole("img", { name: "Weekly: 25%" })).toBe(teamChart);
	expect(screen.queryByRole("img", { name: "Weekly: 90%" })).toBeNull();
});
