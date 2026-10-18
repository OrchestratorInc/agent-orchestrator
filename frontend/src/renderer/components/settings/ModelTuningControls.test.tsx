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
	{ id: "gateway", label: "Gateway", efforts: [] },
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

	it("falls back to the common ladder when the provider reports no efforts", async () => {
		const onEffortChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="gateway"
				effort=""
				onEffortChange={onEffortChange}
				variant="settings"
				roleLabel="Worker"
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Worker Effort" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "Medium" }));
		expect(onEffortChange).toHaveBeenCalledWith("medium");
		expect(screen.getByText(/best guess/i)).toBeInTheDocument();
	});

	it("keeps a saved effort for a gateway model instead of flagging it invalid", () => {
		const onValidityChange = vi.fn();
		const onEffortReset = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="gateway"
				effort="high"
				onEffortChange={vi.fn()}
				onEffortReset={onEffortReset}
				onValidityChange={onValidityChange}
				variant="settings"
			/>,
		);

		expect(screen.queryByRole("alert")).not.toBeInTheDocument();
		expect(onValidityChange).toHaveBeenCalledWith(true);
		expect(onEffortReset).not.toHaveBeenCalled();
	});

	it("falls back to the ladder for an off-catalog model", async () => {
		const onEffortChange = vi.fn();
		render(
			<ModelTuningControls
				models={models}
				model="custom-off-catalog"
				effort=""
				onEffortChange={onEffortChange}
				variant="settings"
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(await screen.findByRole("menuitemradio", { name: "Low" }));
		expect(onEffortChange).toHaveBeenCalledWith("low");
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
