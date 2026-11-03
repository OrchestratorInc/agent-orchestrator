import { render, screen, waitFor } from "@testing-library/react";
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
