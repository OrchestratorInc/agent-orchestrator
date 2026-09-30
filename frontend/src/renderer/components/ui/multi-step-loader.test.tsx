import { fireEvent, render, screen, within } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MultiStepLoader } from "./multi-step-loader";

const steps = ["Building your session", "Connecting to the worker", "Preparing your repository and agent", "Connecting your terminal"] as const;

describe("MultiStepLoader", () => {
	it("shows every stage, a solid completed bar, and its percentage", () => {
		const view = render(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={1} duration={1_000} steps={steps} />);
		const activity = screen.getByRole("status", { name: "Session setup activity" });
		const phrase = within(activity).getByTestId("multi-step-loader-step");
		const animatedPhrase = phrase.querySelector(".multi-step-loader__step");
		expect(animatedPhrase).toHaveStyle("--multi-step-loader-duration: 1000ms");
		for (const step of steps) expect(activity).toHaveTextContent(step);
		expect(phrase).toHaveTextContent(steps[1]);
		const progress = within(activity).getByRole("progressbar");
		expect(progress).toHaveAttribute("aria-valuenow", "33");
		expect(within(progress).getByTestId("multi-step-loader-completed")).toHaveStyle({ width: "33%" });
		expect(within(progress).queryByTestId("multi-step-loader-active")).not.toBeInTheDocument();
		expect(within(activity).getByTestId("multi-step-loader-percent")).toHaveTextContent("33%");
		expect(within(activity).queryByTestId("multi-step-loader-timer")).not.toBeInTheDocument();
		expect(phrase.querySelector(".multi-step-loader__dot")).not.toBeInTheDocument();
		view.rerender(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={2} duration={1_000} steps={steps} />);
		expect(progress).toHaveAttribute("aria-valuenow", "67");
		expect(within(progress).getByTestId("multi-step-loader-completed")).toHaveStyle({ width: "67%" });
		expect(within(activity).getByTestId("multi-step-loader-percent")).toHaveTextContent("67%");
		view.rerender(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={3} duration={1_000} steps={steps} />);
		expect(progress).toHaveAttribute("aria-valuenow", "100");
		expect(within(activity).getByTestId("multi-step-loader-percent")).toHaveTextContent("100%");
	});

	it("changes phrase only when its active stage prop changes", () => {
		const view = render(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={0} steps={steps} />);
		const shimmer = screen.getByTestId("multi-step-loader-step").querySelector(".multi-step-loader__shimmer");
		view.rerender(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={0} steps={steps} />);
		expect(screen.getByTestId("multi-step-loader-step").querySelector(".multi-step-loader__shimmer")).toBe(shimmer);
		fireEvent(screen.getByTestId("multi-step-loader-step"), new window.Event("animationend", { bubbles: true }));
		expect(screen.getByTestId("multi-step-loader-step")).toHaveTextContent(steps[0]);
		view.rerender(<MultiStepLoader ariaLabel="Session setup activity" activeIndex={2} steps={steps} />);
		expect(screen.getByTestId("multi-step-loader-step")).toHaveTextContent(steps[2]);
	});
});
