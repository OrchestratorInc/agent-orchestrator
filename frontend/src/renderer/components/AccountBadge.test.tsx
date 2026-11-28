import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AccountBadge, AccountChip, ProviderMarkWithAccount } from "./AccountBadge";

describe("AccountBadge", () => {
	it("shows the derived letters and is named by the account", () => {
		render(<AccountBadge provider="claude" name="Cristian Account" email="c@example.com" color="blue" />);
		const badge = screen.getByRole("img", { name: "Cristian Account" });
		expect(badge).toHaveTextContent("CA");
		expect(badge).toHaveAttribute("title", "Cristian Account");
	});

	it("falls back to the email for its label when the account has no name", () => {
		render(<AccountBadge provider="codex" email="dana@example.com" />);
		expect(screen.getByRole("img", { name: "dana@example.com" })).toHaveTextContent("DA");
	});

	it("paints the letters segment with the account color token", () => {
		render(<AccountBadge provider="claude" name="Work" color="teal" />);
		const letters = screen.getByText("WO");
		expect(letters.className).toContain("bg-(--account-color-teal)");
		expect(letters.className).toContain("text-(color:--account-badge-foreground)");
	});

	it("is neutral without a color", () => {
		render(<AccountBadge provider="claude" name="Work" />);
		const letters = screen.getByText("WO");
		expect(letters.className).not.toContain("--account-color");
		expect(letters).toHaveClass("bg-foreground/20", "text-foreground");
	});

	it("draws the 14px logo, then the letters in their own square, with no outer frame", () => {
		const { container } = render(<AccountBadge provider="claude" name="Work" />);
		const badge = container.firstElementChild as HTMLElement;
		expect(badge).not.toHaveClass("border");
		const [logo, letters] = Array.from(badge.children) as HTMLElement[];
		expect(logo).toHaveClass("size-3.5");
		expect(letters).toHaveClass("rounded-[5px]");
		expect(letters).not.toHaveClass("border-l");
		expect(letters).toHaveTextContent("WO");
	});

	it("marks the agent's logo with the account's first letter, named by the account", () => {
		render(<ProviderMarkWithAccount provider="claude" name="Work" color="blue" letters="WO" />);
		const mark = screen.getByRole("img", { name: "Work" });
		expect(mark).toHaveTextContent("W");
		expect(mark).not.toHaveTextContent("WO");
		const circle = screen.getByText("W");
		expect(circle).toHaveClass("rounded-full");
		expect(circle.className).toContain("bg-(--account-color-blue)");
		expect(circle.className).not.toContain("ring");
	});

	it("shows a chip's name in plain text beside the letters, with no tint behind it", () => {
		const { container } = render(<AccountChip provider="claude" name="Work" color="blue" label="Work" />);
		const chip = container.firstElementChild as HTMLElement;
		expect(chip.className).not.toContain("bg-");
		expect(screen.getByText("WO").className).toContain("bg-(--account-color-blue)");
		expect(screen.getByText("Work")).toHaveClass("text-foreground");
	});

	it("drops a compact chip's name", () => {
		render(<AccountChip compact provider="claude" name="Work" label="Work" />);
		expect(screen.getByText("WO")).toBeInTheDocument();
		expect(screen.queryByText("Work")).not.toBeInTheDocument();
	});
});
