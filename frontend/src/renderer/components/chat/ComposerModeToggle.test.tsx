import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { ComposerModeToggle } from "./ComposerModeToggle";

describe("ComposerModeToggle", () => {
	it("marks the current mode and switches to the other", () => {
		const onChange = vi.fn();
		render(<TooltipProvider><ComposerModeToggle mode="agent" onChange={onChange} /></TooltipProvider>);
		expect(screen.getByRole("group", { name: "Write to" })).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Agent" })).toHaveAttribute("aria-pressed", "true");
		expect(screen.getByRole("button", { name: "Aside" })).toHaveAttribute("aria-pressed", "false");
		fireEvent.click(screen.getByRole("button", { name: "Agent" }));
		expect(onChange).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("button", { name: "Aside" }));
		expect(onChange).toHaveBeenCalledWith("aside");
	});
});
