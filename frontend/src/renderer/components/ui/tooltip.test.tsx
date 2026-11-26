import { render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { useKeybindingsStore } from "../../stores/keybindings-store";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./tooltip";

function renderOpen(shortcut?: "toggle-sidebar") {
	render(
		<TooltipProvider>
			<Tooltip open>
				<TooltipTrigger>trigger</TooltipTrigger>
				<TooltipContent shortcut={shortcut}>Toggle sidebar</TooltipContent>
			</Tooltip>
		</TooltipProvider>,
	);
}

describe("TooltipContent shortcut", () => {
	afterEach(() => useKeybindingsStore.setState({ overrides: {} }));

	it("shows the command's keybinding beside the label", () => {
		renderOpen("toggle-sidebar");
		const [content] = screen.getAllByText("Toggle sidebar");
		const caps = content.closest("[data-slot=tooltip-content]")?.querySelectorAll("kbd");
		expect(caps?.length).toBeGreaterThan(0);
	});

	it("follows custom keybindings", () => {
		useKeybindingsStore.setState({
			overrides: { "toggle-sidebar": [{ key: "j", ctrl: false, meta: true, shift: false, alt: true }] },
		});
		renderOpen("toggle-sidebar");
		const [content] = screen.getAllByText("Toggle sidebar");
		const text = Array.from(content.closest("[data-slot=tooltip-content]")?.querySelectorAll("kbd") ?? []).map((k) => k.textContent);
		expect(text).toContain("J");
	});

	it("renders plain text when no shortcut is given", () => {
		renderOpen();
		const [content] = screen.getAllByText("Toggle sidebar");
		expect(content.closest("[data-slot=tooltip-content]")?.querySelector("kbd")).toBeNull();
	});
});
