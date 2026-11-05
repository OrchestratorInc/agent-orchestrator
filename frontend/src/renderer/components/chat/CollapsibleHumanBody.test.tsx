import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { CollapsibleHumanBody } from "./CollapsibleHumanBody";

// jsdom has no layout: give every element the same rendered height. With no
// computed line height the fold falls back to 22px lines, so it folds at 220px
// and only when the content is taller than 264px.
function renderedHeight(px: number) {
	vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockReturnValue(px);
}

afterEach(() => vi.restoreAllMocks());

describe("CollapsibleHumanBody", () => {
	it("leaves a message that fits exactly as it was, with no toggle", () => {
		renderedHeight(120);
		render(<CollapsibleHumanBody stateKey="short"><p>Short message</p></CollapsibleHumanBody>);
		expect(screen.getByText("Short message")).toBeInTheDocument();
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
		const clip = screen.getByText("Short message").parentElement!.parentElement!;
		expect(clip).not.toHaveAttribute("style");
		expect(clip).not.toHaveAttribute("data-folded");
	});

	it("does not fold when only a line or two would hide", () => {
		renderedHeight(250);
		render(<CollapsibleHumanBody stateKey="nearly"><p>Nearly long</p></CollapsibleHumanBody>);
		expect(screen.queryByRole("button")).not.toBeInTheDocument();
	});

	it("folds a tall message and opens and closes it", () => {
		renderedHeight(900);
		render(<CollapsibleHumanBody stateKey="tall"><p>Long message</p></CollapsibleHumanBody>);
		const clip = screen.getByText("Long message").parentElement!.parentElement!;
		expect(clip).toHaveAttribute("data-folded");
		expect(clip.style.height).toBe("220px");

		const toggle = screen.getByRole("button", { name: "Show more" });
		expect(toggle).toHaveAttribute("aria-expanded", "false");
		expect(toggle).toHaveAttribute("aria-controls", clip.id);
		fireEvent.click(toggle);
		expect(clip).not.toHaveAttribute("data-folded");
		expect(screen.getByRole("button", { name: "Show less" })).toHaveAttribute("aria-expanded", "true");

		fireEvent.click(screen.getByRole("button", { name: "Show less" }));
		expect(clip).toHaveAttribute("data-folded");
		expect(screen.getByRole("button", { name: "Show more" })).toHaveAttribute("aria-expanded", "false");
	});

	it("starts a remount folded, before anything measures it", () => {
		renderedHeight(900);
		const { unmount } = render(<CollapsibleHumanBody stateKey="handoff"><p>Handed off</p></CollapsibleHumanBody>);
		unmount();
		// The stored copy replacing a local echo mounts a new bubble with the same text.
		renderedHeight(0);
		render(<CollapsibleHumanBody stateKey="handoff"><p>Handed off</p></CollapsibleHumanBody>);
		expect(screen.getByRole("button", { name: "Show more" })).toBeInTheDocument();
	});

	it("comes back open after a remount, as a virtualized chat does when it scrolls away", () => {
		renderedHeight(900);
		const { unmount } = render(<CollapsibleHumanBody stateKey="remount"><p>Opened</p></CollapsibleHumanBody>);
		fireEvent.click(screen.getByRole("button", { name: "Show more" }));
		unmount();
		render(<CollapsibleHumanBody stateKey="remount"><p>Opened</p></CollapsibleHumanBody>);
		expect(screen.getByRole("button", { name: "Show less" })).toBeInTheDocument();
	});
});
