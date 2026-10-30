import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SessionFileTabs } from "./SessionFileTabs";
import { TooltipProvider } from "./ui/tooltip";

describe("SessionFileTabs", () => {
	it("activates and closes language-aware file tabs", async () => {
		const onActivateFile = vi.fn();
		const onCloseFile = vi.fn();
		render(
			<TooltipProvider>
				<div role="tablist">
					<SessionFileTabs
						state={{ openPaths: ["src/App.tsx"], activePath: "src/App.tsx" }}
						onActivateFile={onActivateFile}
						onCloseFile={onCloseFile}
					/>
				</div>
			</TooltipProvider>,
		);
		const tab = screen.getByRole("tab", { name: "App.tsx" });
		expect(tab.querySelector('[aria-hidden="true"]')).toBeInTheDocument();
		const closeButton = screen.getByRole("button", { name: "Close App.tsx" });
		expect(screen.queryByRole("button", { name: "Add feedback for file src/App.tsx" })).not.toBeInTheDocument();
		await userEvent.click(screen.getByText("App.tsx"));
		expect(onActivateFile).toHaveBeenCalledWith("src/App.tsx");
		await userEvent.click(closeButton);
		expect(onCloseFile).toHaveBeenCalledWith("src/App.tsx");
	});

	it("replaces the file icon with a visible dirty dot until the file is saved", () => {
		render(
			<TooltipProvider>
				<div role="tablist">
					<SessionFileTabs
						dirtyPaths={new Set(["src/App.tsx"])}
						state={{ openPaths: ["src/App.tsx"], activePath: "src/App.tsx" }}
						onActivateFile={vi.fn()}
						onCloseFile={vi.fn()}
					/>
				</div>
			</TooltipProvider>,
		);

		// Keyboard focus swaps the dot for the close icon just like hover does.
		expect(screen.getByTestId("unsaved-tab-indicator")).toHaveClass("group-hover:hidden", "group-focus-within:hidden");
		const closeIcon = screen.getByRole("button", { name: "Close App.tsx" }).querySelector("svg:not([data-testid])");
		expect(closeIcon).toHaveClass("hidden", "group-hover:block", "group-focus-within:block");
		expect(screen.getByTestId("unsaved-tab-indicator")).toHaveClass("rounded-full", "bg-foreground", "group-hover:hidden");
		expect(screen.getByRole("button", { name: "Close App.tsx" }).closest("[data-terminal-tab-action]")).not.toHaveClass("opacity-0");
	});
});
