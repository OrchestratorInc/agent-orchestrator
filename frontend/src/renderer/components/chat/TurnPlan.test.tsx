import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { TurnPlan } from "./TurnPlan";
import type { ConversationPlan, PlanStepStatus } from "../../types/conversation";

// The plan is the one panel that changes while the reader is looking at it, so the
// things worth pinning are: every step's state is available to assistive tech rather
// than only as a colour, and the panel is absent rather than empty when the agent
// made no plan.

// The body folds away over 200ms; a loaded machine can stretch that past
// waitFor's 1s default.
const FOLD_WAIT = { timeout: 5000 };

const plan: ConversationPlan = {
	explanation: "Land the store first so the endpoint has something to read.",
	steps: [
		{ text: "Land the conversation store", status: "completed" },
		{ text: "Run the backend tests", status: "in_progress" },
		{ text: "Add the snapshot endpoint", status: "pending" },
	],
};

describe("TurnPlan", () => {
	it("renders the steps as a list with the plan's own explanation", () => {
		render(<TurnPlan plan={plan} />);
		expect(screen.getByRole("list")).toBeInTheDocument();
		expect(screen.getAllByRole("listitem")).toHaveLength(3);
		expect(screen.getByText(/Land the store first/)).toBeInTheDocument();
	});

	it("counts what is done, so progress is readable without reading every row", () => {
		render(<TurnPlan plan={plan} />);
		expect(screen.getByText("1/3")).toBeInTheDocument();
	});

	it("states each step's status in text, not only as a mark", () => {
		render(<TurnPlan plan={plan} />);
		expect(screen.getByLabelText("Land the conversation store — done")).toBeInTheDocument();
		expect(screen.getByLabelText("Run the backend tests — in progress")).toBeInTheDocument();
		expect(screen.getByLabelText("Add the snapshot endpoint — not started")).toBeInTheDocument();
	});

	it("says the plan can still change while the turn runs", () => {
		render(<TurnPlan plan={plan} live />);
		expect(screen.getByRole("region", { name: "Agent plan" })).toHaveAttribute("aria-busy", "true");
	});

	// A finished plan is not still planning, even on a turn that is technically live.
	it("stops claiming to be planning once every step is done", () => {
		render(
			<TurnPlan
				plan={{ steps: [{ text: "Only step", status: "completed" }] }}
				live
			/>,
		);
		expect(screen.getByRole("region", { name: "Agent plan" })).toHaveAttribute("aria-busy", "false");
	});

	it("names the step in progress on the folded header", async () => {
		const user = userEvent.setup();
		render(<TurnPlan plan={plan} live />);

		await user.click(screen.getByRole("button", { name: "Collapse plan" }));
		expect(screen.getByRole("button", { name: "Expand plan" })).toHaveTextContent("Run the backend tests");
	});

	it("renders nothing for a plan with no steps", () => {
		const { container } = render(<TurnPlan plan={{ steps: [] }} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("omits the explanation when the provider gave none", () => {
		render(<TurnPlan plan={{ steps: plan.steps }} />);
		expect(screen.queryByText(/Land the store first/)).not.toBeInTheDocument();
	});

	it("collapses the plan body when the header icon is clicked", async () => {
		const user = userEvent.setup();
		render(<TurnPlan plan={plan} />);

		expect(screen.getByRole("list")).toBeInTheDocument();
		await user.click(screen.getByRole("button", { name: "Collapse plan" }));
		// The body folds away rather than vanishing, so it leaves once the fold ends.
		await waitFor(() => expect(screen.queryByRole("list")).not.toBeInTheDocument(), FOLD_WAIT);
		expect(screen.queryByText(/Land the store first/)).not.toBeInTheDocument();
		expect(screen.getByText("1/3")).toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Expand plan" }));
		expect(screen.getByRole("list")).toBeInTheDocument();
		expect(screen.getByText(/Land the store first/)).toBeInTheDocument();
	});

	it("shows a finished plan folded to its header, and opens it on request", async () => {
		const user = userEvent.setup();
		render(<TurnPlan plan={finished(plan)} />);

		expect(screen.getByText("3/3")).toBeInTheDocument();
		expect(screen.queryByRole("list")).not.toBeInTheDocument();

		await user.click(screen.getByRole("button", { name: "Expand plan" }));
		expect(screen.getByRole("list")).toBeInTheDocument();
	});

	it("folds when the last step is ticked and opens again if the agent re-plans", async () => {
		const { rerender } = render(<TurnPlan plan={plan} live />);
		expect(screen.getByRole("list")).toBeInTheDocument();

		rerender(<TurnPlan plan={finished(plan)} live />);
		await waitFor(() => expect(screen.queryByRole("list")).not.toBeInTheDocument(), FOLD_WAIT);
		expect(screen.getByText("3/3")).toBeInTheDocument();

		rerender(
			<TurnPlan
				plan={{ steps: [...finished(plan).steps, { text: "Fix the flaky test", status: "in_progress" }] }}
				live
			/>,
		);
		expect(await screen.findByRole("list")).toBeInTheDocument();
		expect(screen.getByText("3/4")).toBeInTheDocument();
	});
});

describe("TurnPlan scroll window", () => {
	const ROW = 24;
	const WINDOW = 192;

	afterEach(() => {
		vi.restoreAllMocks();
		delete (Element.prototype as Partial<Element>).scrollTo;
	});

	/** jsdom has no layout: give each step a 24px row and the window 192px. */
	function stubLayout(rows: number) {
		const scrollTo = vi.fn();
		Object.defineProperty(Element.prototype, "scrollTo", { configurable: true, writable: true, value: scrollTo });
		vi.spyOn(Element.prototype, "scrollHeight", "get").mockImplementation(function (this: Element) {
			return this.classList.contains("edge-fade-viewport") ? rows * ROW + 8 : 0;
		});
		vi.spyOn(Element.prototype, "clientHeight", "get").mockImplementation(function (this: Element) {
			return this.classList.contains("edge-fade-viewport") ? WINDOW : 0;
		});
		vi.spyOn(HTMLElement.prototype, "offsetTop", "get").mockImplementation(function (this: HTMLElement) {
			return 4 + Number(this.dataset.planStep ?? 0) * ROW;
		});
		vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(ROW);
		return scrollTo;
	}

	function longPlan(active: number): ConversationPlan {
		return {
			steps: Array.from({ length: 12 }, (_, index) => ({
				text: `Step ${index + 1}`,
				status: (index < active ? "completed" : index === active ? "in_progress" : "pending") as PlanStepStatus,
			})),
		};
	}

	it("centres the step in progress, landing instantly and then following smoothly", () => {
		const scrollTo = stubLayout(12);
		const { rerender, container } = render(<TurnPlan plan={longPlan(7)} live />);

		// Step 8 starts at 4 + 7 × 24 = 172px; centring it in a 192px window puts it 84px down.
		expect(scrollTo).toHaveBeenLastCalledWith({ top: 88, behavior: "auto" });
		const viewport = container.querySelector(".edge-fade-viewport")!;
		expect(viewport).toHaveAttribute("data-fade-bottom");
		expect(viewport).not.toHaveAttribute("data-fade-top");

		rerender(<TurnPlan plan={longPlan(8)} live />);
		// Clamped to the most the window can scroll: 12 × 24 + 8 − 192 = 104px.
		expect(scrollTo).toHaveBeenLastCalledWith({ top: 104, behavior: "smooth" });
	});

	it("leaves a plan that fits alone", () => {
		const scrollTo = stubLayout(3);
		const { container } = render(<TurnPlan plan={plan} live />);

		expect(scrollTo).not.toHaveBeenCalled();
		const viewport = container.querySelector(".edge-fade-viewport")!;
		expect(viewport).not.toHaveAttribute("data-fade-top");
		expect(viewport).not.toHaveAttribute("data-fade-bottom");
	});
});

function finished(source: ConversationPlan): ConversationPlan {
	return { ...source, steps: source.steps.map((step) => ({ ...step, status: "completed" as const })) };
}
