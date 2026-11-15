import { act, fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { expect, it, vi } from "vitest";
import { subscribeBrowserOverlaySync } from "../../lib/browser-overlay-sync";
import {
	OptionMenu,
	OptionMenuContent,
	OptionMenuItem,
	OptionMenuSub,
	OptionMenuSubContent,
	OptionMenuSubTrigger,
	OptionMenuTrigger,
} from "./option-menu";

it.each(["pointer", "keyboard"])("closes only the opted-in submenu after a %s selection", async (input) => {
	const user = userEvent.setup();
	const onSelect = vi.fn();
	render(
		<OptionMenu>
			<OptionMenuTrigger>Turn settings</OptionMenuTrigger>
			<OptionMenuContent>
				<OptionMenuSub closeOnSelect>
					<OptionMenuSubTrigger>Model</OptionMenuSubTrigger>
					<OptionMenuSubContent>
						<OptionMenuItem onSelect={onSelect}>Opus</OptionMenuItem>
						<OptionMenuItem disabled>Unavailable</OptionMenuItem>
						<OptionMenuItem onSelect={(event) => event.preventDefault()}>Other models</OptionMenuItem>
					</OptionMenuSubContent>
				</OptionMenuSub>
			</OptionMenuContent>
		</OptionMenu>,
	);
	await user.click(screen.getByRole("button", { name: "Turn settings" }));
	await user.keyboard("{ArrowDown}{ArrowRight}");
	fireEvent.click(await screen.findByRole("menuitem", { name: "Unavailable" }));
	fireEvent.click(screen.getByRole("menuitem", { name: "Other models" }));
	expect(screen.getAllByRole("menu")).toHaveLength(2);
	const choice = screen.getByRole("menuitem", { name: "Opus" });
	if (input === "keyboard") {
		choice.focus();
		await user.keyboard("{Enter}");
	} else {
		fireEvent.click(choice);
	}
	expect(onSelect).toHaveBeenCalledOnce();
	expect(screen.getAllByRole("menu")).toHaveLength(1);
	expect(screen.queryByRole("menuitem", { name: "Opus" })).not.toBeInTheDocument();
	expect(screen.getByRole("menuitem", { name: "Model" })).toHaveFocus();
	await user.keyboard("{ArrowRight}");
	expect(await screen.findByRole("menuitem", { name: "Opus" })).toBeInTheDocument();
	await user.keyboard("{Escape}");
	expect(screen.queryAllByRole("menu")).toHaveLength(0);
	await user.click(screen.getByRole("button", { name: "Turn settings" }));
	expect(screen.getAllByRole("menu")).toHaveLength(1);
});

it("keeps a submenu a selection closed shut when the hover-open timer from before it fires", () => {
	vi.useFakeTimers();
	try {
		render(
			<OptionMenu defaultOpen>
				<OptionMenuTrigger>Turn settings</OptionMenuTrigger>
				<OptionMenuContent>
					<OptionMenuSub closeOnSelect>
						<OptionMenuSubTrigger>Model</OptionMenuSubTrigger>
						<OptionMenuSubContent>
							<OptionMenuItem>Opus</OptionMenuItem>
						</OptionMenuSubContent>
					</OptionMenuSub>
				</OptionMenuContent>
			</OptionMenu>,
		);
		const model = screen.getByRole("menuitem", { name: "Model" });
		// The pointer arrives on the row (Radix starts its 100ms hover-open timer)
		// and clicks it at once; a choice is made before the timer fires.
		fireEvent.pointerMove(model, { pointerType: "mouse" });
		fireEvent.click(model);
		fireEvent.click(screen.getByRole("menuitem", { name: "Opus" }));
		act(() => vi.advanceTimersByTime(500));
		expect(screen.queryByRole("menuitem", { name: "Opus" })).not.toBeInTheDocument();
		expect(screen.getAllByRole("menu")).toHaveLength(1);
		// Asked for again, it opens.
		fireEvent.click(model);
		act(() => vi.advanceTimersByTime(50));
		expect(screen.getByRole("menuitem", { name: "Opus" })).toBeInTheDocument();
	} finally {
		vi.useRealTimers();
	}
});

it("leaves both ancestor menus open after a nested choice", async () => {
	const user = userEvent.setup();
	render(
		<OptionMenu>
			<OptionMenuTrigger>Turn settings</OptionMenuTrigger>
			<OptionMenuContent>
				<OptionMenuSub closeOnSelect>
					<OptionMenuSubTrigger>More</OptionMenuSubTrigger>
					<OptionMenuSubContent>
						<OptionMenuSub closeOnSelect>
							<OptionMenuSubTrigger>Agent mode</OptionMenuSubTrigger>
							<OptionMenuSubContent>
								<OptionMenuItem>Plan</OptionMenuItem>
							</OptionMenuSubContent>
						</OptionMenuSub>
					</OptionMenuSubContent>
				</OptionMenuSub>
			</OptionMenuContent>
		</OptionMenu>,
	);
	await user.click(screen.getByRole("button", { name: "Turn settings" }));
	await user.keyboard("{ArrowDown}{ArrowRight}");
	const mode = await screen.findByRole("menuitem", { name: "Agent mode" });
	mode.focus();
	await user.keyboard("{ArrowRight}");
	fireEvent.click(await screen.findByRole("menuitem", { name: "Plan" }));
	expect(screen.getAllByRole("menu")).toHaveLength(2);
	expect(screen.queryByRole("menuitem", { name: "Plan" })).not.toBeInTheDocument();
	expect(mode).toHaveFocus();
});

it("keeps option menus and submenus above the native browser until dismissed", async () => {
	const user = userEvent.setup();
	const setOverlayOpen = vi.spyOn(window.ao!.browser, "setOverlayOpen");
	const unsubscribe = subscribeBrowserOverlaySync();
	const onSelect = vi.fn();
	try {
		render(
			<OptionMenu>
				<OptionMenuTrigger>Turn settings</OptionMenuTrigger>
				<OptionMenuContent>
					<OptionMenuSub>
						<OptionMenuSubTrigger>Model</OptionMenuSubTrigger>
						<OptionMenuSubContent>
							<OptionMenuItem onSelect={onSelect}>Opus</OptionMenuItem>
						</OptionMenuSubContent>
					</OptionMenuSub>
				</OptionMenuContent>
			</OptionMenu>,
		);
		await user.click(screen.getByRole("button", { name: "Turn settings" }));
		await waitFor(() => expect(setOverlayOpen).toHaveBeenLastCalledWith(true));
		await user.keyboard("{ArrowDown}{ArrowRight}");
		const choice = await screen.findByRole("menuitem", { name: "Opus" });
		expect(choice.closest('[role="menu"]')).toHaveAttribute("data-browser-native-overlay", "true");
		expect(setOverlayOpen).not.toHaveBeenCalledWith(false);
		await user.click(choice);
		expect(onSelect).toHaveBeenCalledOnce();
		await waitFor(() => expect(setOverlayOpen).toHaveBeenLastCalledWith(false));
		expect(screen.queryByRole("menu")).not.toBeInTheDocument();
	} finally {
		unsubscribe();
		setOverlayOpen.mockRestore();
	}
});
