import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";
import { EffortPicker } from "./EffortPicker";
import { ModelTuningControls } from "./ModelTuningControls";

type Model = components["schemas"]["AgentModelInfo"];

const models: Model[] = [
	{
		id: "capable",
		label: "Capable",
		isDefault: true,
		efforts: ["default", "low", "high"],
		defaultEffort: "low",
	},
	{ id: "plain", label: "Plain", efforts: ["low"] },
];

describe("ModelTuningControls", () => {
	it("shows the reported effort without saving it until selected", async () => {
		const onEffortChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="capable"
				effort=""
				onEffortChange={onEffortChange}
				variant="settings"
				roleLabel="Worker"
			/>,
		);

		const effort = screen.getByRole("button", { name: "Worker Effort" });
		expect(effort).toHaveTextContent("Low");
		expect(onEffortChange).not.toHaveBeenCalled();
		await userEvent.click(effort);
		expect(screen.queryByRole("menuitem", { name: "Provider default" })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "default" })).not.toBeInTheDocument();
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "High" }));
		expect(onEffortChange).toHaveBeenCalledWith("high");
	});

	it("clears incompatible dependent selections when the model changes", () => {
		const onEffortChange = vi.fn();
		const view = render(
			<ModelTuningControls
				models={models}
				model="capable"
				effort="high"
				onEffortChange={onEffortChange}
				variant="composer"
			/>,
		);
		view.rerender(
			<ModelTuningControls
				models={models}
				model="plain"
				effort="high"
				onEffortChange={onEffortChange}
				variant="composer"
			/>,
		);

		expect(onEffortChange).toHaveBeenCalledWith("");
	});

	it("warns and marks unsupported saved values invalid until corrected", () => {
		const onValidityChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="plain"
				effort="high"
				onEffortChange={vi.fn()}
				onValidityChange={onValidityChange}
				variant="settings"
				roleLabel="Reviewer"
			/>,
		);

		expect(screen.getByRole("alert")).toHaveTextContent("Reviewer model tuning is no longer supported");
		expect(onValidityChange).toHaveBeenCalledWith(false);
	});
	it("keeps the effort control and saved choice when capability metadata is missing", async () => {
		const change = vi.fn();
		const validity = vi.fn();
		const view = render(<ModelTuningControls models={models} model="capable" effort="high" onEffortChange={change} onValidityChange={validity} variant="composer" />);
		view.rerender(<ModelTuningControls models={[...models, { id: "unknown", label: "Unknown" }]} model="unknown" effort="high" onEffortChange={change} onValidityChange={validity} variant="composer" />);
		expect(change).not.toHaveBeenCalled();
		expect(validity).toHaveBeenLastCalledWith(true);
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		expect(screen.queryByText("Effort options have not been reported for this model.")).not.toBeInTheDocument();
		expect(screen.queryByText("High (unavailable)")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Clear effort" }));
		expect(change).toHaveBeenLastCalledWith("");
	});

	it("keeps a visible reset control for a model with confirmed empty effort choices", async () => {
		const change = vi.fn();
		const validity = vi.fn();
		render(<ModelTuningControls models={[{ id: "plain", label: "Plain", efforts: [] }]} model="plain" effort="high" onEffortChange={change} onValidityChange={validity} variant="settings" />);
		expect(validity).toHaveBeenLastCalledWith(false);
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		expect(screen.queryByText("This model does not support an effort setting.")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Clear effort" }));
		expect(change).toHaveBeenLastCalledWith("");
	});

	it("does not borrow effort choices for an unlisted custom model", async () => {
		render(<ModelTuningControls models={models} model="custom" effort="" onEffortChange={vi.fn()} variant="composer" />);
		expect(screen.queryByRole("button", { name: "Effort" })).not.toBeInTheDocument();
		expect(screen.queryByRole("menuitemradio", { name: "High" })).not.toBeInTheDocument();
		expect(screen.queryByText("Effort options have not been reported for this model.")).not.toBeInTheDocument();
	});

	it("flags a saved effort for a model the loaded catalog does not list", () => {
		const validity = vi.fn();
		render(<ModelTuningControls models={models} model="custom" effort="high" onEffortChange={vi.fn()} onValidityChange={validity} variant="settings" />);
		expect(validity).toHaveBeenLastCalledWith(false);
	});

	it.each([{ reset: "", expected: "" }, { reset: null, expected: "high" }])("selects the reported effort default with reset=$reset", async ({ reset, expected }) => {
		const change = vi.fn();
		render(<EffortPicker value="" choices={[{ value: "low" }, { value: "high" }]} defaultEffort="high" defaultValue={reset} onChange={change} />);
		const trigger = screen.getByRole("button", { name: "Effort" });
		expect(trigger).toHaveTextContent("High");
		expect(trigger).not.toHaveTextContent("default");
		await userEvent.click(trigger);
		expect(screen.queryByRole("menuitemradio", { name: "Default" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		expect(change).toHaveBeenCalledWith(expected);
	});

});
