import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { appI18n } from "../../i18n";
import { EffortPicker } from "./EffortPicker";

describe("EffortPicker", () => {
	it("shows concrete effort names without default labels", async () => {
		const change = vi.fn();
		render(<EffortPicker value="" choices={[{ value: "low" }, { value: "xhigh" }]} defaultEffort="xhigh" onChange={change} />);
		const trigger = screen.getByRole("button", { name: "Effort" });
		expect(trigger).toHaveTextContent(/^Extra high$/);
		await userEvent.click(trigger);
		expect(screen.queryByRole("menuitemradio", { name: "Default" })).not.toBeInTheDocument();
		expect(screen.getByRole("menuitemradio", { name: "Extra high" })).toHaveAttribute("aria-checked", "true");
		await userEvent.click(screen.getByRole("menuitemradio", { name: "Low" }));
		expect(change).toHaveBeenCalledWith("low");
	});

	it("keeps a separate explicit level when the provider offers no reset", async () => {
		const change = vi.fn();
		render(<EffortPicker value="high" choices={[{ value: "high", label: "High" }]} defaultValue={null} onChange={change} />);
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		expect(screen.queryByRole("menuitemradio", { name: "Default" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		expect(change).toHaveBeenCalledWith("high");
	});

	it("uses the provider reset value when returning to its reported default", async () => {
		const change = vi.fn();
		render(<EffortPicker value="low" choices={[{ value: "default", label: "Provider default" }, { value: "low" }, { value: "high" }]} defaultValue="default" defaultEffort="high" onChange={change} />);
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		await userEvent.click(screen.getByRole("menuitemradio", { name: "High" }));
		expect(change).toHaveBeenCalledWith("default");
	});

	it.each(["unknown", "unsupported", "launch-unavailable"] as const)("keeps %s menus compact and allows clearing a saved value", async (availability) => {
		const change = vi.fn();
		render(<EffortPicker value="high" choices={[]} availability={availability} onChange={change} />);
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		expect(screen.queryByRole("menuitemradio")).not.toBeInTheDocument();
		expect(screen.queryByText(/options have not|does not support/)).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("menuitem", { name: "Clear effort" }));
		expect(change).toHaveBeenCalledWith("");
	});

	it("hides empty controls rather than claiming an effort level", () => {
		render(<EffortPicker value="" choices={[]} availability="unknown" onChange={vi.fn()} />);
		expect(screen.queryByRole("button", { name: "Effort" })).not.toBeInTheDocument();
	});

	it("shows only selectable levels when the current level is unknown", async () => {
		render(<EffortPicker value="" choices={[{ value: "low" }, { value: "high" }]} onChange={vi.fn()} />);
		await userEvent.click(screen.getByRole("button", { name: "Effort" }));
		expect(screen.getAllByRole("menuitemradio").map((item) => item.textContent)).toEqual(["Low", "High"]);
		expect(screen.queryByText(/Default|Use agent effort|Effort not reported/)).not.toBeInTheDocument();
	});

	it("keeps all reported choices and prevents changes while disabled", () => {
		render(<EffortPicker value="high" choices={[{ value: "high" }, { value: "max" }]} disabled onChange={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Effort" })).toBeDisabled();
	});

	it("localizes known level names and leaves unknown provider levels as reported", async () => {
		await appI18n.changeLanguage("fr");
		try {
			render(<EffortPicker value="xhigh" choices={[{ value: "xhigh" }, { value: "turbo" }]} onChange={vi.fn()} />);
			const trigger = screen.getByRole("button", { name: "Effort" });
			expect(trigger).toHaveTextContent("Très élevé");
			await userEvent.click(trigger);
			expect(screen.getByRole("menuitemradio", { name: "Turbo" })).toBeInTheDocument();
		} finally {
			await appI18n.changeLanguage("en");
		}
	});

	it("ignores a prose default description instead of showing it as the label", () => {
		render(<EffortPicker value="" choices={[{ value: "low" }, { value: "high" }]} defaultEffort="Let the provider decide" onChange={vi.fn()} />);
		expect(screen.getByRole("button", { name: "Effort" })).toHaveTextContent(/^Effort$/);
	});
});
