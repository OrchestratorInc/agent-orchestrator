import { fireEvent, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { OptionMenu, OptionMenuContent, OptionMenuItem, OptionMenuTrigger } from "./option-menu";

describe("OptionMenuTrigger press surface", () => {
	afterEach(() => {
		vi.restoreAllMocks();
	});

	it("plays the press on the inner surface, never on the button that anchors the menu", () => {
		const animate = vi.fn();
		Object.defineProperty(HTMLElement.prototype, "animate", { configurable: true, writable: true, value: animate });
		render(
			<OptionMenu>
				<OptionMenuTrigger aria-label="Model" pressSurfaceClassName="px-3">
					<span>GPT</span>
				</OptionMenuTrigger>
				<OptionMenuContent>
					<OptionMenuItem>One</OptionMenuItem>
				</OptionMenuContent>
			</OptionMenu>,
		);
		const button = screen.getByRole("button", { name: "Model" });
		const surface = screen.getByText("GPT").parentElement!;
		expect(button).toHaveAttribute("data-press-host");
		expect(surface).toHaveClass("px-3");
		expect(button).not.toHaveClass("px-3");

		fireEvent.pointerDown(button, { button: 0, pointerType: "mouse" });
		expect(animate).toHaveBeenCalledTimes(1);
		expect(animate.mock.contexts[0]).toBe(surface);

		fireEvent.keyDown(button, { key: "Enter" });
		expect(animate).toHaveBeenCalledTimes(2);
		expect(animate.mock.contexts[1]).toBe(surface);
	});
});
