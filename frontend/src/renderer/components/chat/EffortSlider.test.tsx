import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { OptionMenu, OptionMenuContent } from "../ui/option-menu";
import { EffortSlider } from "./EffortSlider";

const LEVELS = [
	{ value: "low", label: "Low" },
	{ value: "medium", label: "Medium" },
	{ value: "high", label: "High" },
];

function slider(props: { choices?: typeof LEVELS; value?: string; onCommit?: (value: string) => void }) {
	return (
		<OptionMenu open>
			<OptionMenuContent>
				<EffortSlider
					label="Effort"
					valueLabel={(choice) => choice?.label ?? "Default"}
					choices={props.choices ?? LEVELS}
					value={props.value}
					onCommit={props.onCommit ?? (() => {})}
				/>
			</OptionMenuContent>
		</OptionMenu>
	);
}

describe("EffortSlider", () => {
	it("sends a move back to the reported level while another is still being applied", () => {
		const onCommit = vi.fn();
		render(slider({ value: "low", onCommit }));
		const effort = screen.getByRole("slider", { name: "Effort" });
		fireEvent.keyDown(effort, { key: "ArrowRight" });
		expect(effort).toHaveAttribute("aria-valuetext", "Medium");
		// The agent has not reported Medium yet; Low is asked for again, or Medium is what lands.
		fireEvent.keyDown(effort, { key: "ArrowLeft" });
		expect(onCommit.mock.calls).toEqual([["medium"], ["low"]]);
		expect(effort).toHaveAttribute("aria-valuetext", "Low");
	});

	it("does not resend the level already asked for", () => {
		const onCommit = vi.fn();
		render(slider({ value: "low", onCommit }));
		const effort = screen.getByRole("slider", { name: "Effort" });
		fireEvent.keyDown(effort, { key: "End" });
		fireEvent.keyDown(effort, { key: "End" });
		fireEvent.keyDown(effort, { key: "Home" });
		fireEvent.keyDown(effort, { key: "Home" });
		expect(onCommit.mock.calls).toEqual([["high"], ["low"]]);
	});

	it("drops a held level when the model's levels change", () => {
		const { rerender } = render(slider({ value: "low" }));
		const effort = screen.getByRole("slider", { name: "Effort" });
		fireEvent.keyDown(effort, { key: "End" });
		expect(effort).toHaveAttribute("aria-valuetext", "High");
		// Another model, with fewer levels and the same reported one: the held third level is gone.
		rerender(slider({ value: "low", choices: LEVELS.slice(0, 2) }));
		expect(screen.getByRole("slider", { name: "Effort" })).toHaveAttribute("aria-valuetext", "Low");
	});
});
