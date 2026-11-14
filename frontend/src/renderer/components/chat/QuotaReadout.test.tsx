import { act, render, renderHook, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { formatQuotaReset, nearQuotaWindow, QUOTA_FLASH_MS, QuotaMenuNotice, QuotaReadout, QuotaStatus, useQuotaFlash, type QuotaWindow } from "./QuotaReadout";
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

	it("keeps the warning and when it resets at the top of the open menu", () => {
		const { rerender } = render(<QuotaMenuNotice quota={{ percent: 82, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByRole("status")).toHaveTextContent("Approaching limit");
		expect(screen.getByRole("status")).toHaveTextContent("Resets in 2h 15m");

		rerender(<QuotaMenuNotice quota={{ percent: 100, kind: "session", resetsIn: 8_100 }} />);
		expect(screen.getByRole("status")).toHaveTextContent("Usage limit reached");
		expect(screen.getByRole("status")).toHaveAttribute("data-severity", "full");
	});
});

describe("useQuotaFlash", () => {
	beforeEach(() => { vi.useFakeTimers(); });
	afterEach(() => { vi.useRealTimers(); });

	const warn: QuotaWindow = { percent: 80, kind: "session" };
	const flash = (quota: QuotaWindow | undefined, scope = "chat-a") => ({ quota, scope });

	it("shows the warning when a chat opens close to the limit, then steps aside", () => {
		const { result } = renderHook(({ quota, scope }) => useQuotaFlash(quota, scope), { initialProps: flash(warn) });
		expect(result.current).toBe(true);
		act(() => { vi.advanceTimersByTime(QUOTA_FLASH_MS); });
		expect(result.current).toBe(false);
	});

	it("does not flash again for a new reading in the same chat", () => {
		const { result, rerender } = renderHook(({ quota, scope }) => useQuotaFlash(quota, scope), { initialProps: flash(warn) });
		act(() => { vi.advanceTimersByTime(QUOTA_FLASH_MS); });
		rerender(flash({ percent: 88, kind: "session" }));
		expect(result.current).toBe(false);
	});

	it("flashes again when usage crosses the line anew, or another chat opens", () => {
		const { result, rerender } = renderHook(({ quota, scope }) => useQuotaFlash(quota, scope), { initialProps: flash(warn) });
		act(() => { vi.advanceTimersByTime(QUOTA_FLASH_MS); });
		rerender(flash(undefined));
		rerender(flash(warn));
		expect(result.current).toBe(true);

		act(() => { vi.advanceTimersByTime(QUOTA_FLASH_MS); });
		rerender(flash(warn, "chat-b"));
		expect(result.current).toBe(true);
	});

	it("re-arms a dismissed chat after briefly visiting another chat", () => {
		const { result, rerender } = renderHook(({ quota, scope }) => useQuotaFlash(quota, scope), { initialProps: flash(warn) });
		act(() => { vi.advanceTimersByTime(QUOTA_FLASH_MS); });
		expect(result.current).toBe(false);
		rerender(flash(warn, "chat-b"));
		act(() => { vi.advanceTimersByTime(1000); });
		rerender(flash(warn));
		expect(result.current).toBe(true);
		act(() => { vi.advanceTimersByTime(QUOTA_FLASH_MS); });
		expect(result.current).toBe(false);
		rerender(flash({ ...warn, percent: 85 }));
		expect(result.current).toBe(false);
	});

	it("holds a full window until it resets", () => {
		const { result } = renderHook(({ quota, scope }) => useQuotaFlash(quota, scope), { initialProps: flash({ percent: 100, kind: "session" }) });
		act(() => { vi.advanceTimersByTime(QUOTA_FLASH_MS * 3); });
		expect(result.current).toBe(true);
	});
});
