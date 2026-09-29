import { act, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { MultiStepLoader } from "./multi-step-loader";

const steps = ["Building your session", "Connecting to the worker", "Preparing your repository and agent", "Connecting your terminal"] as const;

afterEach(() => vi.useRealTimers());

describe("MultiStepLoader", () => {
	it("keeps the original single animated phrase and a muted elapsed timer", () => {
		vi.useFakeTimers();
		vi.setSystemTime(new Date("2026-01-01T00:00:12Z"));
		render(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={1} activeSince="2026-01-01T00:00:05Z" duration={1_000} steps={steps} />);
		const activity = screen.getByRole("status", { name: "Session setup activity" });
		const phrase = within(activity).getByTestId("multi-step-loader-step");
		const animatedPhrase = phrase.querySelector(".multi-step-loader__step");
		expect(animatedPhrase).toHaveStyle("--multi-step-loader-duration: 1000ms");
		expect(phrase.querySelector(".multi-step-loader__dot")).toBeInTheDocument();
		expect(phrase.querySelector(".multi-step-loader__check")).not.toBeInTheDocument();
		expect(phrase).toHaveTextContent(steps[1]);
		expect(activity).not.toHaveTextContent(steps[0]);
		expect(activity).not.toHaveTextContent(steps[2]);
		expect(within(activity).getByTestId("multi-step-loader-timer")).toHaveTextContent("0:07");
		expect(within(activity).getByTestId("multi-step-loader-timer")).toHaveClass("text-muted-foreground/65");
		act(() => vi.advanceTimersByTime(1_000));
		expect(within(activity).getByTestId("multi-step-loader-timer")).toHaveTextContent("0:08");
		expect(phrase.querySelector(".multi-step-loader__step")).toBe(animatedPhrase);
	});

	it("changes phrase only when its active stage prop changes", () => {
		const view = render(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={0} steps={steps} />);
		fireEvent(screen.getByTestId("multi-step-loader-step"), new window.Event("animationend", { bubbles: true }));
		expect(screen.getByTestId("multi-step-loader-step")).toHaveTextContent(steps[0]);
		view.rerender(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={2} steps={steps} />);
		expect(screen.getByTestId("multi-step-loader-step")).toHaveTextContent(steps[2]);
	});
});
