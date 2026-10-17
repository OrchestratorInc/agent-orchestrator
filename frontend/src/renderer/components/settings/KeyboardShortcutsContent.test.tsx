import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useKeybindingsStore } from "../../stores/keybindings-store";
import { TooltipProvider } from "../ui/tooltip";
import { KeyboardShortcutsContent } from "./KeyboardShortcutsContent";

const originalResetAll = useKeybindingsStore.getState().resetAll;

function renderContent() {
	return render(
		<TooltipProvider>
			<KeyboardShortcutsContent active isMac={false} />
		</TooltipProvider>,
	);
}

describe("KeyboardShortcutsContent", () => {
	beforeEach(() => {
		useKeybindingsStore.setState({
			overrides: { "toggle-terminal": [{ key: "j", ctrl: true, meta: false, shift: false, alt: false }] },
			loaded: true,
		});
	});

	afterEach(() => {
		useKeybindingsStore.setState({ resetAll: originalResetAll });
	});

	// Reset all wipes every custom shortcut, so a stray Enter must not confirm
	// it, and a failed reset keeps the dialog open with the reason.
	it("opens the Reset all confirm on Cancel and reports a failed reset", async () => {
		const user = userEvent.setup();
		const resetAll = vi.fn(async () => {
			throw new Error("disk full");
		});
		useKeybindingsStore.setState({ resetAll });
		renderContent();

		await user.click(screen.getByRole("button", { name: "Reset all" }));
		const dialog = await screen.findByRole("dialog", { name: "Reset all" });
		await waitFor(() => expect(within(dialog).getByRole("button", { name: "Cancel" })).toHaveFocus());

		await user.click(within(dialog).getByRole("button", { name: "Reset all" }));
		expect(await within(dialog).findByRole("alert")).toHaveTextContent("Could not reset shortcuts");
		expect(resetAll).toHaveBeenCalledTimes(1);
		expect(screen.getByRole("dialog", { name: "Reset all" })).toBeInTheDocument();
	});

	it("closes the confirm and restores defaults once the reset succeeds", async () => {
		const user = userEvent.setup();
		const resetAll = vi.fn(async () => undefined);
		useKeybindingsStore.setState({ resetAll });
		renderContent();

		await user.click(screen.getByRole("button", { name: "Reset all" }));
		const dialog = await screen.findByRole("dialog", { name: "Reset all" });
		await user.click(within(dialog).getByRole("button", { name: "Reset all" }));

		await waitFor(() => expect(screen.queryByRole("dialog", { name: "Reset all" })).not.toBeInTheDocument());
		expect(resetAll).toHaveBeenCalledTimes(1);
		expect(await screen.findByRole("status")).toHaveTextContent("Keyboard shortcuts restored");
	});
});
