import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionFileTab } from "./SessionFileTabs";
import { TabContextMenu } from "./TabContextMenu";
import { TooltipProvider } from "./ui/tooltip";

// A file tab as the session strip renders it: inside its tab menu.
function renderFileTab({ dirty = false } = {}) {
	const onActivate = vi.fn();
	const onClose = vi.fn();
	render(
		<TooltipProvider>
			<div role="tablist">
				<TabContextMenu
					spec={{ kind: "file", tabKey: "file:src/App.tsx", onClose, file: { path: "src/App.tsx", sessionId: "sess-1", remote: false } }}
				>
					<SessionFileTab active dirty={dirty} onActivate={onActivate} onClose={onClose} path="src/App.tsx" />
				</TabContextMenu>
			</div>
		</TooltipProvider>,
	);
	return { onActivate, onClose };
}

describe("SessionFileTab", () => {
	it("activates on click, shows its ⋮ menu, and closes on a middle-click", async () => {
		const { onActivate, onClose } = renderFileTab();
		const tab = screen.getByRole("tab", { name: "App.tsx" });
		expect(tab.querySelector('[aria-hidden="true"]')).toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Tab actions" })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "Add feedback for file src/App.tsx" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByText("App.tsx"));
		expect(onActivate).toHaveBeenCalled();
		fireEvent(tab.closest("[data-terminal-tab-frame]")!, new MouseEvent("auxclick", { bubbles: true, button: 1 }));
		expect(onClose).toHaveBeenCalledOnce();
	});

	it("replaces the file icon with a visible dirty dot until the file is saved", () => {
		renderFileTab({ dirty: true });

		// Keyboard focus swaps the dot for the ⋮ icon just like hover does.
		expect(screen.getByTestId("unsaved-tab-indicator")).toHaveClass("group-hover:hidden", "group-has-[:focus-visible]:hidden");
		const menuIcon = screen.getByRole("button", { name: "Tab actions" }).querySelector("svg:not([data-testid])");
		expect(menuIcon).toHaveClass("hidden", "group-hover:block", "group-has-[:focus-visible]:block");
		expect(screen.getByTestId("unsaved-tab-indicator")).toHaveClass("rounded-full", "bg-foreground", "group-hover:hidden");
		expect(screen.getByRole("button", { name: "Tab actions" }).closest("[data-terminal-tab-action]")).not.toHaveClass("opacity-0");
	});
});
