import { fireEvent, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { ChatAnnotationSummary } from "./ChatAnnotationSummary";

describe("ChatAnnotationSummary", () => {
	it("opens once on pointer click and remains closed after row selection", async () => {
		const user = userEvent.setup();
		render(<ChatAnnotationSummary annotations={[{ text: "selected text" }]} />);
		const trigger = screen.getByRole("button", { name: "1 annotation" });
		expect(trigger).toHaveClass("w-fit", "self-start");
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
		expect(item.closest("div")).toHaveClass("border", "rounded-md");
		fireEvent.click(item);
		expect(onSelect).toHaveBeenCalledWith({ id: "one", text: "first selected text" });
	});

	it("opens when the count receives focus", () => {
		render(<ChatAnnotationSummary annotations={[{ id: "one", text: "selected text" }]} />);
		fireEvent.focus(screen.getByRole("button", { name: "1 annotation" }));
		expect(screen.getByRole("button", { name: "selected text" })).toBeInTheDocument();
	});
});
