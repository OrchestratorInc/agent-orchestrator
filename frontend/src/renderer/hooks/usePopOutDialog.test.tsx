import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { SessionBrowserPopOut } from "../components/SessionBrowserPopOut";
import { SessionFilesPopOut } from "../components/SessionFilesPopOut";
import { usePopOutDialog } from "./usePopOutDialog";

function Overlay({ onClose }: { onClose?: () => void }) {
	const props = usePopOutDialog<HTMLDivElement>(onClose);
	return (
		<div {...props} aria-label="overlay">
			<input aria-label="inner" />
			<input
				aria-label="nested"
				onKeyDown={(event) => {
					if (event.key === "Escape") event.preventDefault();
				}}
			/>
		</div>
	);
}

describe("usePopOutDialog", () => {
	it("exposes modal dialog semantics and moves focus in", () => {
		render(<Overlay onClose={() => undefined} />);
		const dialog = screen.getByRole("dialog", { name: "overlay" });
		expect(dialog).toHaveAttribute("aria-modal", "true");
		expect(dialog).toHaveAttribute("tabindex", "-1");
		expect(document.activeElement).toBe(dialog);
	});

	it("restores focus to the opener on unmount", () => {
		const opener = document.createElement("button");
		document.body.append(opener);
		opener.focus();
		const { unmount } = render(<Overlay />);
		expect(document.activeElement).not.toBe(opener);
		unmount();
		expect(document.activeElement).toBe(opener);
		opener.remove();
	});

	it("calls onClose on Escape and stops it propagating", () => {
		const onClose = vi.fn();
		const outer = vi.fn();
		render(<div onKeyDown={outer}><Overlay onClose={onClose} /></div>);
		fireEvent.keyDown(screen.getByLabelText("inner"), { key: "Escape" });
		expect(onClose).toHaveBeenCalledTimes(1);
		expect(outer).not.toHaveBeenCalled();
	});

	it("ignores Escape that a nested control already handled", () => {
		const onClose = vi.fn();
		render(<Overlay onClose={onClose} />);
		fireEvent.keyDown(screen.getByLabelText("nested"), { key: "Escape" });
		expect(onClose).not.toHaveBeenCalled();
	});

	it("ignores other keys", () => {
		const onClose = vi.fn();
		render(<Overlay onClose={onClose} />);
		fireEvent.keyDown(screen.getByLabelText("inner"), { key: "a" });
		expect(onClose).not.toHaveBeenCalled();
	});
});

describe("pop-out overlays", () => {
	it("SessionBrowserPopOut is a dialog that closes on Escape", () => {
		const onClose = vi.fn();
		render(<SessionBrowserPopOut onClose={onClose} onTopbarHost={() => undefined} phase="open"><button>child</button></SessionBrowserPopOut>);
		const dialog = screen.getByRole("dialog");
		expect(dialog).toHaveAttribute("aria-modal", "true");
		expect(dialog).toHaveAccessibleName();
		fireEvent.keyDown(screen.getByText("child"), { key: "Escape" });
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it("SessionFilesPopOut is a dialog that closes on Escape", () => {
		const onClose = vi.fn();
		render(<SessionFilesPopOut onClose={onClose}>{() => <button>child</button>}</SessionFilesPopOut>);
		const dialog = screen.getByRole("dialog");
		expect(dialog).toHaveAttribute("aria-modal", "true");
		expect(dialog).toHaveAccessibleName();
		fireEvent.keyDown(screen.getByText("child"), { key: "Escape" });
		expect(onClose).toHaveBeenCalledTimes(1);
	});
});
