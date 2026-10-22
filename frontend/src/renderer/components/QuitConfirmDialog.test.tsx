import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { QuitConfirmDialog } from "./QuitConfirmDialog";

test("asks to quit AO with Quit focused so Return confirms", async () => {
	const onResolve = vi.fn();
	render(<QuitConfirmDialog open onResolve={onResolve} />);

	expect(screen.getByRole("dialog", { name: "Quit AO?" })).toHaveAccessibleDescription("Are you sure you want to quit AO?");
	const quit = screen.getByRole("button", { name: "Quit" });
	await waitFor(() => expect(quit).toHaveFocus());
	expect(quit).toHaveAttribute("aria-keyshortcuts", "Enter");

	fireEvent.click(quit);
	expect(onResolve).toHaveBeenCalledExactlyOnceWith(true);
});

test("Cancel and Escape both dismiss without quitting", () => {
	const onResolve = vi.fn();
	render(<QuitConfirmDialog open onResolve={onResolve} />);

	const cancel = screen.getByRole("button", { name: "Cancel" });
	expect(cancel).toHaveAttribute("aria-keyshortcuts", "Escape");
	fireEvent.click(cancel);
	fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
	expect(onResolve.mock.calls).toEqual([[false], [false]]);
});
