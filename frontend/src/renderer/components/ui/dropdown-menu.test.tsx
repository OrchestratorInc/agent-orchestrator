import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "./dropdown-menu";

// A click inside an artifact preview goes to its cross-origin iframe. Chromium
// sends this document no pointer or focusin event for it: only the window's
// blur, with the frame left as the active element. jsdom would dispatch focusin
// on frame.focus(), which Radix already treats as outside, so set it directly.
function clickIntoFrame(tag: "iframe" | "webview" = "iframe") {
	const frame = document.createElement(tag);
	document.body.append(frame);
	Object.defineProperty(document, "activeElement", { configurable: true, get: () => frame });
	window.dispatchEvent(new Event("blur"));
	return frame;
}

function Menu(props: { open?: boolean; onOpenChange?: (open: boolean) => void }) {
	return (
		<DropdownMenu {...props}>
			<DropdownMenuTrigger>Actions</DropdownMenuTrigger>
			<DropdownMenuContent>
				<DropdownMenuItem>Pin artifact</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}

function ControlledMenu({ onOpenChange }: { onOpenChange: (open: boolean) => void }) {
	const [open, setOpen] = useState(false);
	return <Menu open={open} onOpenChange={(next) => { setOpen(next); onOpenChange(next); }} />;
}

describe("DropdownMenu", () => {
	afterEach(() => {
		// Drops the own-property override, back to Document.prototype's getter.
		delete (document as { activeElement?: Element | null }).activeElement;
		document.querySelectorAll("iframe, webview").forEach((frame) => frame.remove());
	});

	it("closes when a click lands in a framed page", async () => {
		render(<Menu />);
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		expect(await screen.findByRole("menuitem", { name: "Pin artifact" })).toBeInTheDocument();
		clickIntoFrame();
		await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
	});

	it("closes when a webview takes focus", async () => {
		render(<Menu />);
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		await screen.findByRole("menu");
		clickIntoFrame("webview");
		await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
	});

	it("tells a controlling caller that it closed", async () => {
		const onOpenChange = vi.fn();
		render(<ControlledMenu onOpenChange={onOpenChange} />);
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		await screen.findByRole("menu");
		clickIntoFrame();
		await waitFor(() => expect(screen.queryByRole("menu")).toBeNull());
		expect(onOpenChange).toHaveBeenLastCalledWith(false);
	});

	it("stays open when the window loses focus to another app", async () => {
		render(<Menu />);
		await userEvent.click(screen.getByRole("button", { name: "Actions" }));
		await screen.findByRole("menu");
		window.dispatchEvent(new Event("blur"));
		await new Promise((resolve) => setTimeout(resolve, 10));
		expect(screen.getByRole("menu")).toBeInTheDocument();
	});
});
