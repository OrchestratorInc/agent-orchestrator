import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { AccountBadge } from "./AccountBadge";

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
		expect(letters.className).toContain("text-(--account-badge-foreground)");
	});

	it("is neutral without a color", () => {
		render(<AccountBadge provider="claude" name="Work" />);
		const letters = screen.getByText("WO");
		expect(letters.className).not.toContain("--account-color");
		expect(letters).toHaveClass("bg-muted", "text-foreground");
	});

	it("keeps the chip's 14px logo size", () => {
		const { container } = render(<AccountBadge provider="claude" name="Work" />);
		expect(container.firstElementChild).toHaveClass("h-3.5");
	});
});
