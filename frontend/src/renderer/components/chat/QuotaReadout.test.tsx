import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
import { formatQuotaReset, nearQuotaWindow, QuotaMenuNotice, QuotaSign, QuotaStatus, type QuotaWindow } from "./QuotaReadout";
import type { ConversationRateLimits } from "../../types/conversation";

function limits(over: Partial<ConversationRateLimits> = {}): ConversationRateLimits {
	return { primaryUsedPercent: 40, secondaryUsedPercent: -1, primaryResetsInSeconds: 8_100, secondaryResetsInSeconds: 345_600, ...over };
}

describe("QuotaReadout", () => {
	it("only warns from 82%", () => {
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 81 }))).toBeUndefined();
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 82 }))?.kind).toBe("session");
		expect(nearQuotaWindow(undefined)).toBeUndefined();
	});

	it("follows the tighter window and ignores unreported ones", () => {
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 30, secondaryUsedPercent: 88 }))?.kind).toBe("weekly");
		// Negative is the daemon's "not reported"; it must never count as usage.
		expect(nearQuotaWindow(limits({ primaryUsedPercent: -1, secondaryUsedPercent: -1 }))).toBeUndefined();
	});

	it("breaks equal usage ties with the later reported reset", () => {
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 100, secondaryUsedPercent: 100 }))?.kind).toBe("weekly");
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 100, secondaryUsedPercent: 100, primaryResetsInSeconds: 500_000 }))?.kind).toBe("session");
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 100, secondaryUsedPercent: 99 }))?.kind).toBe("session");
		expect(nearQuotaWindow(limits({ primaryUsedPercent: 100, secondaryUsedPercent: 100, secondaryResetsInSeconds: undefined }))?.kind).toBe("session");
	});

	it("keeps a polite region mounted across warnings, exhaustion and model changes", () => {
		const view = (quota: QuotaWindow | undefined, model = "Opus") => <><span>{model}</span><QuotaStatus quota={quota} /></>;
		const { rerender } = render(view(undefined));
		const status = screen.getByRole("status");
		expect(status).toBeEmptyDOMElement();
		expect(status).toHaveAttribute("aria-live", "polite");
		rerender(view({ percent: 80, kind: "session" }));
		expect(screen.getByRole("status")).toBe(status);
		expect(status).toHaveTextContent("Approaching limit");
		rerender(view({ percent: 100, kind: "session", resetsIn: 8100 }));
		expect(status).toHaveTextContent("Usage limit reached. Resets in 2h 15m");
		const text = status.textContent;
		rerender(view({ percent: 100, kind: "session", resetsIn: 8100 }, "Sonnet"));
		expect(status.textContent).toBe(text);
		rerender(view(undefined));
		expect(screen.getByRole("status")).toBe(status);
		expect(status).toBeEmptyDOMElement();
	});

	it("is a warning sign, or a clock once full, with no text beside the model", () => {
		const { rerender } = render(<QuotaSign quota={{ percent: 85, kind: "session", resetsIn: 8_100 }} />);
		const sign = screen.getByRole("button", { name: /Approaching limit/ });
		expect(sign).toHaveAttribute("data-severity", "warn");
		expect(sign).toHaveTextContent("");

		rerender(<QuotaSign quota={{ percent: 100, kind: "weekly" }} />);
		expect(screen.getByRole("button", { name: /Usage limit reached/ })).toHaveAttribute("data-severity", "full");
	});

	it("shows the usage and reset on hover, in two lines and without the model", async () => {
		render(<QuotaSign quota={{ percent: 85, kind: "session", resetsIn: 8_100 }} />);
		await userEvent.hover(screen.getByRole("button", { name: /Approaching limit/ }));
		const tip = await screen.findByRole("tooltip");
		expect(tip).toHaveTextContent("Approaching limit");
		expect(tip).toHaveTextContent("Session usage limit 85% used. Resets in 2h 15m.");
		expect(tip).not.toHaveTextContent(/Opus|Sonnet/);
	});

	it("formats resets to the useful unit", () => {
		expect(formatQuotaReset(20)).toBe("1m");
		expect(formatQuotaReset(2_400)).toBe("40m");
		expect(formatQuotaReset(7_200)).toBe("2h");
		expect(formatQuotaReset(8_100)).toBe("2h 15m");
		expect(formatQuotaReset(4 * 86_400)).toBe("4d");
	});

	it("keeps the warning and when it resets at the top of the open menu", () => {
		const { rerender } = render(<QuotaMenuNotice quota={{ percent: 82, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByRole("status")).toHaveTextContent("Approaching limit");
		expect(screen.getByRole("status")).toHaveTextContent("Resets in 2h 15m");

		rerender(<QuotaMenuNotice quota={{ percent: 100, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByRole("status")).toHaveTextContent("Usage limit reached");
		expect(screen.getByRole("status")).toHaveAttribute("data-severity", "full");
	});
});
