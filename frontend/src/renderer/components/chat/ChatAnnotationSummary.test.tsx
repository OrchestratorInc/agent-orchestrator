import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import { ChatAnnotationSummary } from "./ChatAnnotationSummary";

describe("ChatAnnotationSummary", () => {
	it("switches between main and side chat popovers with one click in either direction", async () => {
		const user = userEvent.setup();
		render(<>
			<ChatAnnotationSummary annotations={[{ id: "main", text: "main excerpt" }]} />
			<ChatAnnotationSummary annotations={[{ id: "side", text: "side excerpt" }, { id: "other", text: "other side excerpt" }]} />
		</>);
		const main = screen.getByRole("button", { name: "1 annotation" });
		const side = screen.getByRole("button", { name: "2 annotations" });
		await user.click(side);
		expect(screen.getByRole("button", { name: "side excerpt" })).toBeInTheDocument();
		await user.click(main);
		await waitFor(() => expect(main).toHaveAttribute("aria-expanded", "true"));
		expect(side).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("button", { name: "main excerpt" })).toHaveFocus();
		await user.click(side);
		await waitFor(() => expect(side).toHaveAttribute("aria-expanded", "true"));
		expect(main).toHaveAttribute("aria-expanded", "false");
		expect(screen.getByRole("button", { name: "side excerpt" })).toHaveFocus();
	});
	it("preserves focus on an outside click and restores it after Escape on reopening", async () => {
		const user = userEvent.setup();
		render(<>
			<ChatAnnotationSummary annotations={[{ id: "one", text: "excerpt" }]} />
			<button type="button">Outside action</button>
		</>);
		const trigger = screen.getByRole("button", { name: "1 annotation" });
		const outside = screen.getByRole("button", { name: "Outside action" });
		await user.click(trigger);
		await user.click(outside);
		await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "false"));
		expect(outside).toHaveFocus();
		await user.click(trigger);
		await user.keyboard("{Escape}");
		await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "false"));
		expect(trigger).toHaveFocus();
	});
	it("opens once on pointer click and remains closed after row selection", async () => {
		const user = userEvent.setup();
		render(<ChatAnnotationSummary annotations={[{ text: "selected text" }]} />);
		const trigger = screen.getByRole("button", { name: "1 annotation" });
		expect(trigger.parentElement).toHaveClass("w-fit", "self-start");
		await user.click(trigger);
		expect(trigger).toHaveAttribute("aria-expanded", "true");
		await user.click(screen.getByRole("button", { name: "selected text" }));
		await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "false"));
		expect(trigger).toHaveFocus();
		await user.click(trigger);
		expect(trigger).toHaveAttribute("aria-expanded", "true");
		await user.keyboard("{Escape}");
		await waitFor(() => expect(trigger).toHaveAttribute("aria-expanded", "false"));
	});
	it("shows the compact count and a clickable annotation list", () => {
		const onSelect = vi.fn();
		render(
			<ChatAnnotationSummary
				annotations={[{ id: "one", text: "first selected text" }, { id: "two", text: "second selected text" }]}
				onSelect={onSelect}
			/>,
		);

		const trigger = screen.getByRole("button", { name: "2 annotations" });
		expect(trigger).toHaveTextContent("2 annotations");
		fireEvent.click(trigger);
		const item = screen.getByRole("button", { name: "first selected text" });
		expect(item.closest("div")).toHaveClass("rounded", "bg-interactive-hover");
		expect(item.closest("div")).not.toHaveClass("border");
		fireEvent.click(item);
		expect(onSelect).toHaveBeenCalledWith({ id: "one", text: "first selected text" });
	});

	it("removes without navigating, focuses the next then previous control, and hides when empty", async () => {
		const user = userEvent.setup();
		const onSelect = vi.fn();
		function EditableSummary() {
			const [annotations, setAnnotations] = useState([
				{ id: "one", text: "first" }, { id: "two", text: "second" }, { id: "three", text: "third" },
			]);
			return <ChatAnnotationSummary annotations={annotations} onSelect={onSelect} onRemove={(removed) => setAnnotations((items) => items.filter((item) => item.id !== removed.id))} />;
		}
		render(<EditableSummary />);
		await user.click(screen.getByRole("button", { name: "3 annotations" }));
		await user.click(screen.getByRole("button", { name: "Remove annotation 2" }));
		expect(screen.queryByRole("button", { name: "second" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Remove annotation 2" })).toHaveFocus();
		expect(screen.getByRole("button", { name: "2 annotations" })).toHaveAttribute("aria-expanded", "true");
		await user.keyboard("{Enter}");
		expect(screen.queryByRole("button", { name: "third" })).not.toBeInTheDocument();
		expect(screen.getByRole("button", { name: "Remove annotation 1" })).toHaveFocus();
		await user.keyboard("{Enter}");
		expect(screen.queryByRole("button", { name: "1 annotation" })).not.toBeInTheDocument();
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(onSelect).not.toHaveBeenCalled();
	});

	it("truncates to one line without showing a full-text tooltip", async () => {
		const user = userEvent.setup();
		const text = "A long annotation excerpt\nwith several lines\nand more context\nthat should remain available in full.";
		render(<ChatAnnotationSummary annotations={[{ id: "one", text }]} />);
		await user.click(screen.getByRole("button", { name: "1 annotation" }));
		const item = screen.getByRole("button", { name: /A long annotation excerpt/ });
		expect(item.querySelector("span")).toHaveClass("truncate");
		expect(item).not.toHaveAttribute("title");
		expect(screen.getByRole("dialog")).toHaveClass("border-(--color-border-overlay)");
		await user.hover(item);
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
		await user.unhover(item);
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.getByRole("button", { name: "1 annotation" })).toHaveFocus());
		await user.keyboard("{Enter}");
		expect(screen.queryByRole("tooltip")).not.toBeInTheDocument();
	});

	it("keeps sent references read-only and disables an open summary's controls", async () => {
		const user = userEvent.setup();
		const annotations = [{ id: "one", text: "selected text" }];
		const onSelect = vi.fn();
		const onRemove = vi.fn();
		const { rerender } = render(<ChatAnnotationSummary annotations={annotations} onSelect={onSelect} />);
		await user.click(screen.getByRole("button", { name: "1 annotation" }));
		expect(screen.queryByRole("button", { name: /Remove annotation/ })).not.toBeInTheDocument();
		rerender(<ChatAnnotationSummary annotations={annotations} onSelect={onSelect} onRemove={onRemove} disabled />);
		const remove = screen.getByRole("button", { name: "Remove annotation 1" });
		expect(remove).toBeDisabled();
		expect(remove).toHaveClass("absolute", "right-1", "top-1/2", "-translate-y-1/2");
		expect(remove.parentElement).toHaveClass("relative");
		expect(screen.getByRole("button", { name: "selected text" })).toHaveClass("w-full", "pr-7");
		expect(remove.querySelector("svg")).toHaveClass("size-3");
		expect(screen.getByRole("button", { name: "selected text" })).toBeDisabled();
		await user.click(remove);
		expect(onRemove).not.toHaveBeenCalled();
		expect(onSelect).not.toHaveBeenCalled();
	});

	it("clears the selected annotations from the chip without opening the popover", async () => {
		const user = userEvent.setup();
		const onSelect = vi.fn();
		const onRemoveAll = vi.fn();
		function EditableSummary() {
			const [annotations, setAnnotations] = useState([{ id: "one", text: "first" }, { id: "two", text: "second" }]);
			return <ChatAnnotationSummary annotations={annotations} onSelect={onSelect} onRemoveAll={() => { onRemoveAll(); setAnnotations([]); }} />;
		}
		render(<EditableSummary />);
		const trigger = screen.getByRole("button", { name: "2 annotations" });
		const clear = screen.getByRole("button", { name: "Remove all annotations" });
		expect(trigger.parentElement).toBe(clear.parentElement);
		expect(trigger.parentElement).toHaveClass("hover:bg-interactive-active");
		expect(trigger).toHaveClass("w-full", "pr-7");
		expect(trigger).not.toHaveClass("hover:bg-interactive-active");
		await user.click(screen.getByRole("button", { name: "Remove all annotations" }));
		expect(onRemoveAll).toHaveBeenCalledOnce();
		expect(onSelect).not.toHaveBeenCalled();
		expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
		expect(screen.queryByRole("button", { name: "2 annotations" })).not.toBeInTheDocument();
	});

	it("disables bulk removal and keeps read-only references without a bulk X", async () => {
		const onRemoveAll = vi.fn();
		const annotations = [{ id: "one", text: "first" }];
		const { rerender } = render(<ChatAnnotationSummary annotations={annotations} onRemoveAll={onRemoveAll} disabled />);
		const clear = screen.getByRole("button", { name: "Remove all annotations" });
		expect(clear).toHaveClass("absolute", "right-1", "top-1/2", "-translate-y-1/2");
		expect(clear.parentElement).toHaveClass("relative", "inline-flex");
		expect(clear.querySelector("svg")).toHaveClass("size-3");
		expect(clear).toBeDisabled();
		await userEvent.click(clear);
		expect(onRemoveAll).not.toHaveBeenCalled();
		rerender(<ChatAnnotationSummary annotations={annotations} />);
		expect(screen.queryByRole("button", { name: "Remove all annotations" })).not.toBeInTheDocument();
	});

	it("does not open on focus; Enter opens it", async () => {
		const user = userEvent.setup();
		render(<ChatAnnotationSummary annotations={[{ id: "one", text: "selected text" }]} />);
		await user.tab();
		const trigger = screen.getByRole("button", { name: "1 annotation" });
		expect(trigger).toHaveFocus();
		expect(trigger).toHaveAttribute("aria-expanded", "false");
		expect(screen.queryByRole("button", { name: "selected text" })).not.toBeInTheDocument();
		await user.keyboard("{Enter}");
		expect(screen.getByRole("button", { name: "selected text" })).toBeInTheDocument();
	});
});
