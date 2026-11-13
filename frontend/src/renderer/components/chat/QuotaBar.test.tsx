import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { formatQuotaReset, nearQuotaWindow, QuotaBar, tightestQuotaWindow } from "./QuotaBar";
import type { ConversationRateLimits } from "../../types/conversation";

function limits(over: Partial<ConversationRateLimits> = {}): ConversationRateLimits {
	return { primaryUsedPercent: 40, secondaryUsedPercent: -1, primaryResetsInSeconds: 8_100, secondaryResetsInSeconds: 345_600, ...over };
}

describe("QuotaBar", () => {
	it("only takes the picker's place from 75%", () => {
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 74 }))).toBeUndefined();
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 75 }))?.kind).toBe("session");
		expect(nearQuotaWindow(undefined)).toBeUndefined();
	});

	it("follows the tighter window and ignores unreported ones", () => {
		expect(tightestQuotaWindow(limits({ primaryUsedPercent: 30, secondaryUsedPercent: 88 }))?.kind).toBe("weekly");
		// Negative is the daemon's "not reported"; drawing it would run the bar backwards.
		expect(tightestQuotaWindow(limits({ primaryUsedPercent: -1, secondaryUsedPercent: -1 }))).toBeUndefined();
	});

	it("draws the real percentage, amber then red past 90%", () => {
		const { rerender } = render(<QuotaBar quota={{ percent: 82, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByRole("meter", { name: "Session" })).toHaveAttribute("aria-valuenow", "82");
		expect(screen.getByText("82%").className).toContain("text-status-in-review");

		rerender(<QuotaBar quota={{ percent: 94, kind: "weekly" }} />);
		expect(screen.getByRole("meter", { name: "Weekly" })).toHaveAttribute("aria-valuenow", "94");
		expect(screen.getByText("94%").className).toContain("text-status-exited");
	});

	it("says when it resets once full", () => {
		render(<QuotaBar quota={{ percent: 100, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByText("Resets in 2h 15m")).toBeInTheDocument();
		expect(screen.queryByText("100%")).not.toBeInTheDocument();
	});

	it("formats resets to the useful unit", () => {
		expect(formatQuotaReset(20)).toBe("1m");
		expect(formatQuotaReset(2_400)).toBe("40m");
		expect(formatQuotaReset(7_200)).toBe("2h");
		expect(formatQuotaReset(8_100)).toBe("2h 15m");
		expect(formatQuotaReset(4 * 86_400)).toBe("4d");
	});
});
