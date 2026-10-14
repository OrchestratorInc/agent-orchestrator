import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { OptionMenu, OptionMenuContent, OptionMenuTrigger } from "../ui/option-menu";
import { SpeedMenu } from "./SpeedMenu";

describe("Speed selector", () => {
	it("defaults to Normal and disables unadvertised acceleration", async () => {
		const user = userEvent.setup();
		render(<OptionMenu><OptionMenuTrigger>Models</OptionMenuTrigger><OptionMenuContent><SpeedMenu onChange={vi.fn()} /></OptionMenuContent></OptionMenu>);
		await user.click(screen.getByRole("button", { name: "Models" }));
		await user.click(screen.getByRole("menuitem", { name: /^Speed/ }));
		expect(screen.getByRole("menuitemradio", { name: "Normal" })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("menuitemradio", { name: /^Fast/ })).toHaveAttribute("aria-disabled", "true");
		expect(screen.getByRole("menuitemradio", { name: /^Ultra-fast/ })).toHaveAttribute("aria-disabled", "true");
	});
	it("selects advertised Ultrafast independently of model and effort", async () => {
		const user = userEvent.setup(); const onChange = vi.fn();
		render(<OptionMenu><OptionMenuTrigger>Models</OptionMenuTrigger><OptionMenuContent><SpeedMenu onChange={onChange} tiers={[{ id: "ultrafast", name: "Ultrafast" }]} /></OptionMenuContent></OptionMenu>);
		await user.click(screen.getByRole("button", { name: "Models" }));
		await user.click(screen.getByRole("menuitem", { name: /^Speed/ }));
		expect(screen.getByText("Pro 25x required for personal subscriptions")).toBeInTheDocument();
		fireEvent.click(screen.getByRole("menuitemradio", { name: /^Ultra-fast/ }));
		expect(onChange).toHaveBeenCalledWith("ultrafast");
	});
});
