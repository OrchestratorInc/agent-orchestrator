import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { UsageLimitNotice } from "./UsageLimitNotice";

const now = new Date("2026-10-14T15:00:00Z");
const resetsAt = new Date(now.getTime() + 65_000).toISOString();

beforeEach(() => {
	vi.useFakeTimers();
	vi.setSystemTime(now);
});

afterEach(() => vi.useRealTimers());

describe("UsageLimitNotice", () => {
	it("shows the local reset time and enables Resume when the countdown expires", () => {
		const onResume = vi.fn();
		const onAutoResumeChange = vi.fn();
		render(<UsageLimitNotice usageLimit={{ resetsAt, autoResume: false }} onResume={onResume} onAutoResumeChange={onAutoResumeChange} />);

		expect(screen.getByText("Usage limit reached")).toHaveClass("text-warning");
		expect(document.querySelector("time")).toHaveAttribute("dateTime", resetsAt);
		expect(document.querySelector("time")).toHaveTextContent(new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(new Date(resetsAt)));
		expect(screen.getByText("1m 5s")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Resume" })).toBeDisabled();

		act(() => vi.advanceTimersByTime(65_000));

		expect(screen.getByRole("status")).toHaveTextContent("Reset time reached. You can resume now.");
		expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: "Auto-resume at reset" })).not.toBeInTheDocument();
		expect(onResume).not.toHaveBeenCalled();
		expect(onAutoResumeChange).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: "Resume" }));
		expect(onResume).toHaveBeenCalledOnce();
	});

	it("opts into automatic resume and lets the user cancel it", () => {
		const onAutoResumeChange = vi.fn();
		const view = render(<UsageLimitNotice usageLimit={{ resetsAt, autoResume: false }} onAutoResumeChange={onAutoResumeChange} />);

		fireEvent.click(screen.getByRole("button", { name: "Auto-resume at reset" }));
		expect(onAutoResumeChange).toHaveBeenLastCalledWith(true);

		view.rerender(<UsageLimitNotice usageLimit={{ resetsAt, autoResume: true }} onAutoResumeChange={onAutoResumeChange} />);
		expect(screen.getByText(/Auto-resume is scheduled/)).toBeInTheDocument();
		fireEvent.click(screen.getByRole("button", { name: "Cancel auto-resume" }));
		expect(onAutoResumeChange).toHaveBeenLastCalledWith(false);
	});

	it("keeps scheduled auto-resume cancellable after the reset time arrives", () => {
		render(<UsageLimitNotice usageLimit={{ resetsAt, autoResume: true }} onResume={vi.fn()} onAutoResumeChange={vi.fn()} />);
		act(() => vi.advanceTimersByTime(65_000));

		expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
		expect(screen.getByRole("button", { name: "Cancel auto-resume" })).toBeEnabled();
	});

	it.each([undefined, "not-a-date"])("offers only manual resume when the reset time is %s", (resetsAt) => {
		render(<UsageLimitNotice usageLimit={{ resetsAt, autoResume: true }} onResume={vi.fn()} onAutoResumeChange={vi.fn()} />);

		expect(screen.getByText("The provider did not supply a reset time.")).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
		expect(screen.queryByRole("button", { name: /Auto-resume/ })).not.toBeInTheDocument();
		expect(screen.queryByText(/Auto-resume is scheduled/)).not.toBeInTheDocument();
	});

	it.each([{ pending: true }, { disabled: true }])("disables both actions while unavailable: %j", (props) => {
		render(<UsageLimitNotice usageLimit={{ resetsAt, autoResume: true }} onResume={vi.fn()} onAutoResumeChange={vi.fn()} {...props} />);

		expect(screen.getByRole("button", { name: "Resume" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Cancel auto-resume" })).toBeDisabled();
	});

	it("shows resume failures beside the actions and prefers the latest request error", () => {
		const view = render(<UsageLimitNotice usageLimit={{ autoResume: false, resumeError: "Automatic resume failed" }} onResume={vi.fn()} />);
		expect(screen.getByRole("alert")).toHaveTextContent("Automatic resume failed");

		view.rerender(<UsageLimitNotice usageLimit={{ autoResume: false, resumeError: "Automatic resume failed" }} onResume={vi.fn()} error="Resume request failed" />);
		expect(screen.getByRole("alert")).toHaveTextContent("Resume request failed");
	});

	it("keeps the historical marker free of controls and countdown timers", () => {
		render(<UsageLimitNotice compact usageLimit={{ resetsAt, autoResume: false }} onResume={vi.fn()} onAutoResumeChange={vi.fn()} />);

		expect(screen.getByText("Usage limit reached")).toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		expect(document.querySelector("time")).not.toBeInTheDocument();
		expect(vi.getTimerCount()).toBe(0);
	});

	it("refreshes an expired reset immediately when returning to the window", () => {
		render(<UsageLimitNotice usageLimit={{ resetsAt, autoResume: false }} onResume={vi.fn()} />);
		vi.setSystemTime(new Date(resetsAt));

		fireEvent.focus(window);

		expect(screen.getByRole("button", { name: "Resume" })).toBeEnabled();
	});
});
