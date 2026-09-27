import { fireEvent, render, screen } from "@testing-library/react";
import { expect, test, vi } from "vitest";
import { ConfirmDialog } from "./ConfirmDialog";

test("keeps confirmation fields in a scrollable body separate from the footer", () => {
	render(<ConfirmDialog open title="Uninstall Codex?" description="Choose the original method." confirmLabel="Uninstall" onConfirm={vi.fn()} onOpenChange={vi.fn()}>
		<label><input type="checkbox" />I confirm the original method</label>
	</ConfirmDialog>);
	const body = screen.getByRole("checkbox").closest(".overflow-y-auto");
	expect(body).toHaveClass("min-h-0");
	expect(body).not.toContainElement(screen.getByRole("button", { name: "Uninstall" }));
});

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

	expect(dialog).toHaveClass("left-[50%]", "top-[50%]", "z-overlay", "bg-popover", "p-0");
	expect(screen.getByRole("button", { name: "Confirm kill" }).querySelector("svg")).toBeNull();
});
