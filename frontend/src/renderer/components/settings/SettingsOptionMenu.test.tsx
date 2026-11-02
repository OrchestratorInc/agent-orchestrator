import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { previewDocumentThemeStyle, type ThemeStyle } from "../../lib/theme";
import { useUiStore } from "../../stores/ui-store";
import { SettingsOptionMenu } from "./SettingsOptionMenu";

describe("SettingsOptionMenu", () => {
	it("filters searchable options by label and value", async () => {
		render(
			<SettingsOptionMenu
				aria-label="Worker model"
				value=""
				searchable
				options={[
					{ value: "anthropic/claude-sonnet", label: "Claude Sonnet" },
					{ value: "openai/gpt-5.5", label: "GPT Five" },
				]}
				onChange={() => {}}
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Worker model" }));
		const search = screen.getByRole("searchbox", { name: "Search worker model" });

		await userEvent.type(search, "sonnet");
		expect(screen.getByRole("menuitem", { name: "Claude Sonnet" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "GPT Five" })).not.toBeInTheDocument();

		await userEvent.clear(search);
		await userEvent.type(search, "openai/gpt");
		expect(screen.getByRole("menuitem", { name: "GPT Five" })).toBeInTheDocument();
		expect(screen.queryByRole("menuitem", { name: "Claude Sonnet" })).not.toBeInTheDocument();
	});

	it("reports the highlighted option and clears it when the menu closes", async () => {
		const onHighlight = vi.fn();
		render(
			<SettingsOptionMenu
				aria-label="Color theme"
				value="github"
				options={[
					{ value: "github", label: "GitHub" },
					{ value: "nord", label: "Nord" },
				]}
				onChange={() => {}}
				onHighlight={onHighlight}
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Color theme" }));
		await userEvent.hover(screen.getByRole("menuitem", { name: "Nord" }));
		expect(onHighlight).toHaveBeenLastCalledWith("nord");

		await userEvent.keyboard("{Escape}");
		expect(onHighlight).toHaveBeenLastCalledWith(null);
	});

	it("clears the highlight when the menu unmounts while open", async () => {
		const onHighlight = vi.fn();
		const { unmount } = render(
			<SettingsOptionMenu
				aria-label="Color theme"
				value="github"
				options={[
					{ value: "github", label: "GitHub" },
					{ value: "nord", label: "Nord" },
				]}
				onChange={() => {}}
				onHighlight={onHighlight}
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Color theme" }));
		await userEvent.hover(screen.getByRole("menuitem", { name: "Nord" }));
		unmount();

		expect(onHighlight).toHaveBeenLastCalledWith(null);
	});

	it.each(["select", "escape"])("ignores hover previews during the closing animation after %s", async (dismiss) => {
		// JSDOM has no CSS animations or live computed styles. Keep Radix's real
		// Presence mounted through the same exit animation as the desktop menu.
		const getComputedStyle = window.getComputedStyle.bind(window);
		const styles = vi.spyOn(window, "getComputedStyle").mockImplementation((element, pseudo) => {
			const style = getComputedStyle(element, pseudo);
			if (element.getAttribute("role") === "menu") {
				Object.defineProperty(style, "animationName", {
					get: () => element.getAttribute("data-state") === "closed" ? "popover-out" : "popover-in",
				});
			}
			return style;
		});
		useUiStore.setState({ themeStyle: "github" });
		const ThemePicker = () => {
			const value = useUiStore((state) => state.themeStyle);
			return (
				<SettingsOptionMenu<ThemeStyle>
					aria-label="Color theme"
					value={value}
					options={[
						{ value: "github", label: "GitHub" },
						{ value: "nord", label: "Nord" },
						{ value: "dracula", label: "Dracula" },
					]}
					onChange={(next) => useUiStore.getState().setThemeStyle(next)}
					onHighlight={(next) => previewDocumentThemeStyle(next, useUiStore.getState().themeStyle)}
				/>
			);
		};
		try {
			render(<ThemePicker />);
			await userEvent.click(screen.getByRole("button", { name: "Color theme" }));
			await userEvent.hover(screen.getByRole("menuitem", { name: "Nord" }));
			expect(document.documentElement.dataset.styleTheme).toBe("nord");
			if (dismiss === "select") {
				await userEvent.click(screen.getByRole("menuitem", { name: "Nord" }));
			} else {
				await userEvent.keyboard("{Escape}");
			}
			const saved = dismiss === "select" ? "nord" : "github";
			expect(screen.getByRole("button", { name: "Color theme" })).toHaveTextContent(dismiss === "select" ? "Nord" : "GitHub");
			expect(screen.getByRole("menu")).toHaveAttribute("data-state", "closed");
			await userEvent.hover(screen.getByRole("menuitem", { name: "Dracula" }));
			expect(useUiStore.getState().themeStyle).toBe(saved);
			expect(document.documentElement.dataset.styleTheme).toBe(saved);

			// Finish the exit, reopen, and make sure previews work for the next visit.
			fireEvent(screen.getByRole("menu"), Object.assign(new Event("animationend", { bubbles: true }), { animationName: "popover-out" }));
			expect(screen.queryByRole("menu")).not.toBeInTheDocument();
			await userEvent.click(screen.getByRole("button", { name: "Color theme" }));
			await userEvent.hover(screen.getByRole("menuitem", { name: "Dracula" }));
			expect(document.documentElement.dataset.styleTheme).toBe("dracula");
			await userEvent.keyboard("{Escape}");
			expect(document.documentElement.dataset.styleTheme).toBe(saved);
		} finally {
			cleanup();
			styles.mockRestore();
			previewDocumentThemeStyle(null, "orchestrate");
			delete document.documentElement.dataset.styleTheme;
			useUiStore.setState({ themeStyle: "orchestrate" });
			window.localStorage.removeItem("ao.theme-style");
		}
	});

	it("keeps an action footer outside the shrinking options region", async () => {
		render(
			<SettingsOptionMenu
				aria-label="Agent"
				value=""
				options={Array.from({ length: 20 }, (_, index) => ({
					value: `agent-${index}`,
					label: `Agent ${index}`,
				}))}
				action={{ label: "Manage agents…", onSelect: () => {} }}
				onChange={() => {}}
			/>,
		);

		await userEvent.click(screen.getByRole("button", { name: "Agent" }));

		const firstOption = screen.getByRole("menuitem", { name: "Agent 0" });
		const action = screen.getByRole("menuitem", { name: "Manage agents…" });
		const scrollRegion = firstOption.parentElement?.parentElement;
		const actionRegion = action.parentElement;

		expect(scrollRegion).toHaveAttribute("data-slot", "settings-option-menu-scroll-region");
		expect(scrollRegion).toHaveClass("min-h-0", "flex-1", "overflow-hidden");
		expect(firstOption.parentElement).toHaveClass("min-h-0", "overflow-y-auto");
		expect(actionRegion).toHaveAttribute("data-slot", "settings-option-menu-action");
		expect(actionRegion).toHaveClass("shrink-0");
	});
});
