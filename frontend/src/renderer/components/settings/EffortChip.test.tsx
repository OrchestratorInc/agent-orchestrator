import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { EffortChip } from "./EffortChip";

describe("EffortChip", () => {
	it("shows the level with a meter and orders known levels low to high", async () => {
		const change = vi.fn();
		render(<EffortChip aria-label="Worker Effort" value="high" levels={[{ value: "max" }, { value: "low" }, { value: "high" }, { value: "medium" }]} onChange={change} />);
		const trigger = screen.getByRole("button", { name: "Worker Effort" });
		expect(trigger).toHaveTextContent(/^High$/);
		const meter = within(trigger).getByTestId("effort-meter");
		expect(meter).toHaveAttribute("data-filled", "3");
		expect(meter.children).toHaveLength(4);
		await userEvent.click(trigger);
		expect(screen.getAllByRole("menuitemradio").map((item) => item.textContent)).toEqual(["Low", "Medium", "High", "Max"]);
		expect(screen.getByRole("menuitemradio", { name: "High" })).toHaveAttribute("aria-checked", "true");
		await userEvent.click(screen.getByRole("menuitemradio", { name: "Max" }));
		expect(change).toHaveBeenCalledWith("max");
	});

	it("keeps a provider's own order for levels AO does not know", async () => {
		render(<EffortChip aria-label="Worker mode" value="ultra" levels={[{ value: "low", label: "Low" }, { value: "ultra", label: "Ultra" }, { value: "medium", label: "Medium" }]} onChange={vi.fn()} />);
		const trigger = screen.getByRole("button", { name: "Worker mode" });
		expect(trigger).toHaveTextContent(/^Ultra$/);
		expect(within(trigger).getByTestId("effort-meter")).toHaveAttribute("data-filled", "2");
	});

	it("renders nothing without levels", () => {
		const { container } = render(<EffortChip aria-label="Worker Effort" value="" levels={[]} onChange={vi.fn()} />);
		expect(container).toBeEmptyDOMElement();
	});
});
