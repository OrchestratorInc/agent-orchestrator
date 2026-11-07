import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { TooltipProvider } from "../ui/tooltip";
import { AsideComposerHint, ComposerModeToggle } from "./ComposerModeToggle";

describe("ComposerModeToggle", () => {
	it("is a radio pair: marks the current mode and switches to the other", () => {
		const onChange = vi.fn();
		render(<TooltipProvider><ComposerModeToggle mode="agent" onChange={onChange} /></TooltipProvider>);
		expect(screen.getByRole("radiogroup", { name: "Write to" })).toBeInTheDocument();
		expect(screen.getByRole("radio", { name: "Agent" })).toHaveAttribute("aria-checked", "true");
		expect(screen.getByRole("radio", { name: "Aside" })).toHaveAttribute("aria-checked", "false");
		expect(screen.getByRole("radio", { name: "Agent" })).toHaveAttribute("tabindex", "0");
		expect(screen.getByRole("radio", { name: "Aside" })).toHaveAttribute("tabindex", "-1");
		fireEvent.click(screen.getByRole("radio", { name: "Agent" }));
		expect(onChange).not.toHaveBeenCalled();
		fireEvent.click(screen.getByRole("radio", { name: "Aside" }));
		expect(onChange).toHaveBeenCalledWith("aside");
	});

	it("moves with the arrow keys, as a radio group does", () => {
		const onChange = vi.fn();
		render(<TooltipProvider><ComposerModeToggle mode="agent" onChange={onChange} /></TooltipProvider>);
		fireEvent.keyDown(screen.getByRole("radio", { name: "Agent" }), { key: "ArrowRight" });
		expect(onChange).toHaveBeenCalledWith("aside");
	});

	it("marks Agent with a needs-you dot while you write an aside and the agent waits", () => {
		const { rerender, container } = render(<TooltipProvider><ComposerModeToggle agentNeedsYou mode="aside" onChange={vi.fn()} /></TooltipProvider>);
		expect(screen.getByRole("radio", { name: "Agent, needs you" })).toHaveAttribute("data-needs-you");
		// In Agent mode the approval is in front of you: no dot.
		rerender(<TooltipProvider><ComposerModeToggle agentNeedsYou mode="agent" onChange={vi.fn()} /></TooltipProvider>);
		expect(container.querySelector("[data-needs-you]")).toBeNull();
	});
});

describe("AsideComposerHint", () => {
	it("says the agent can't see it, in a color that reads", () => {
		const { container } = render(<AsideComposerHint />);
		const hint = container.querySelector("[data-aside-composer-hint]")!;
		expect(hint).toHaveTextContent("Agent can't see this");
		expect(hint.className).toContain("text-muted-foreground");
		expect(hint.className).not.toContain("text-passive");
	});

	it("says asides are hidden on this device, with Show", () => {
		const onShow = vi.fn();
		const { container } = render(<AsideComposerHint hidden onShow={onShow} />);
		expect(container.querySelector("[data-aside-hidden-notice]")).toHaveTextContent("Asides are hidden · Show");
		fireEvent.click(screen.getByRole("button", { name: "Show" }));
		expect(onShow).toHaveBeenCalled();
	});
});
