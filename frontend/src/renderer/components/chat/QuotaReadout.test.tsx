import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { formatQuotaReset, nearQuotaWindow, QuotaReadout, tightestQuotaWindow } from "./QuotaReadout";
import type { ConversationRateLimits } from "../../types/conversation";

function limits(over: Partial<ConversationRateLimits> = {}): ConversationRateLimits {
	return { primaryUsedPercent: 40, secondaryUsedPercent: -1, primaryResetsInSeconds: 8_100, secondaryResetsInSeconds: 345_600, ...over };
}

describe("QuotaReadout", () => {
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

	it("warns without a percentage once close", () => {
		const { rerender } = render(<QuotaReadout quota={{ percent: 82, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByText("Approaching limit")).toBeInTheDocument();
		expect(screen.queryByText(/%/)).not.toBeInTheDocument();

		rerender(<QuotaReadout quota={{ percent: 94, kind: "weekly" }} />);
		expect(screen.getByText("Approaching weekly limit")).toBeInTheDocument();
		expect(screen.queryByText(/%/)).not.toBeInTheDocument();
	});

	it("is a clock and the time left once full", () => {
		render(<QuotaReadout quota={{ percent: 100, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByLabelText("Resets in 2h 15m")).toHaveTextContent(/^2h 15m$/);
		expect(screen.queryByText("Approaching limit")).not.toBeInTheDocument();
		expect(screen.queryByText("100%")).not.toBeInTheDocument();
	});

	it("names the limit when a full window has no reported reset", () => {
		render(<QuotaReadout quota={{ percent: 100, kind: "weekly" }} />);
		expect(screen.getByText("Usage limit reached")).toBeInTheDocument();
	});

	it("formats resets to the useful unit", () => {
		expect(formatQuotaReset(20)).toBe("1m");
		expect(formatQuotaReset(2_400)).toBe("40m");
		expect(formatQuotaReset(7_200)).toBe("2h");
		expect(formatQuotaReset(8_100)).toBe("2h 15m");
		expect(formatQuotaReset(4 * 86_400)).toBe("4d");
	});
});
