import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { ConfirmDialog } from "./ConfirmDialog";

test("can disable confirmation without preventing cancellation", () => {
	const onConfirm = vi.fn();
	const onOpenChange = vi.fn();
	render(<ConfirmDialog open title="Choose a method" description="Select the original method." confirmLabel="Update" confirmDisabled onConfirm={onConfirm} onOpenChange={onOpenChange} />);
	expect(screen.getByRole("button", { name: "Update" })).toBeDisabled();
	fireEvent.click(screen.getByRole("button", { name: "Update" }));
	expect(onConfirm).not.toHaveBeenCalled();
	fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
	expect(onOpenChange).toHaveBeenCalledWith(false);
});

test("starts on Cancel for destructive confirms and restores focus on close", async () => {
	const opener = document.createElement("button");
	document.body.appendChild(opener);
	opener.focus();
	const props = { title: "Kill?", description: "Sure?", confirmLabel: "Kill it", onConfirm: vi.fn(), onOpenChange: vi.fn() };
	const { rerender } = render(<ConfirmDialog {...props} open destructive />);
	await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus());
	expect(screen.getByRole("button", { name: "Close dialog" })).toHaveAttribute("tabindex", "-1");

	rerender(<ConfirmDialog {...props} open={false} destructive />);
	await waitFor(() => expect(opener).toHaveFocus());
	opener.remove();
});

test("ignores Escape while busy", () => {
	const onOpenChange = vi.fn();
	render(<ConfirmDialog open busy title="Kill?" description="Sure?" confirmLabel="Kill it" onConfirm={vi.fn()} onOpenChange={onOpenChange} />);
	fireEvent.keyDown(screen.getByRole("dialog"), { key: "Escape" });
	expect(onOpenChange).not.toHaveBeenCalled();
});

test("uses the same centered settings-dialog layout as nearby app dialogs", () => {
	render(
		<ConfirmDialog
			open
			title="Kill session?"
			description="This will stop the session."
			confirmLabel="Confirm kill"
			destructive
			busy
			onConfirm={vi.fn()}
			onOpenChange={vi.fn()}
		/>,
	);

	const dialog = screen.getByRole("dialog", { name: "Kill session?" });

	expect(dialog).toHaveClass("left-[50%]", "top-[50%]", "z-overlay", "bg-overlay", "p-0");
	expect(screen.getByRole("button", { name: "Confirm kill" }).querySelector("svg")).toBeNull();
});
