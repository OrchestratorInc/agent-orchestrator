import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { MenuEntries } from "./menu-model";
import { DropdownMenu, DropdownMenuContent, DropdownMenuTrigger } from "./ui/dropdown-menu";

function renderMenu(entries: Parameters<typeof MenuEntries>[0]["entries"]) {
	render(
		<DropdownMenu open>
			<DropdownMenuTrigger>Menu</DropdownMenuTrigger>
			<DropdownMenuContent className="w-48">
				<MenuEntries entries={entries} />
			</DropdownMenuContent>
		</DropdownMenu>,
	);
}

describe("MenuEntries", () => {
	// A fixed-width menu must not grow with a row, so a disabled row's reason is
	// its tooltip rather than a line under it, and the row still takes the pointer.
	it("shows a disabled row's reason as a tooltip it can be pointed at", () => {
		renderMenu([{ kind: "item", id: "fork", label: "Fork session", disabled: true, title: "Wait for the agent to go idle.", onSelect: () => undefined }]);
		const item = screen.getByRole("menuitem", { name: "Fork session" });
		expect(item).toHaveAttribute("title", "Wait for the agent to go idle.");
		expect(item).toHaveAttribute("data-disabled");
		expect(item).toHaveClass("data-[disabled]:pointer-events-auto");
	});

	it("leaves a disabled row without a reason ignoring the pointer", () => {
		renderMenu([{ kind: "item", id: "copy", label: "Copy path", disabled: true, onSelect: () => undefined }]);
		expect(screen.getByRole("menuitem", { name: "Copy path" })).not.toHaveClass("data-[disabled]:pointer-events-auto");
	});

	it("truncates a long label instead of widening the menu", () => {
		renderMenu([{ kind: "item", id: "long", label: "A label far too long to fit in the menu", onSelect: () => undefined }]);
		expect(screen.getByText("A label far too long to fit in the menu")).toHaveClass("min-w-0", "truncate");
	});
});
