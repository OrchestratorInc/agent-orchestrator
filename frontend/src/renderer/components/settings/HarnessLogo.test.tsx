import { render, screen } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { HarnessLogo } from "./HarnessLogo";

describe("HarnessLogo", () => {
	it("uses the shared 32px neutral tile and a 24px mark", () => {
		const { container } = render(<HarnessLogo provider="codex" />);
		const tile = container.firstElementChild;
		const mark = container.querySelector("img");

		expect(tile).toHaveClass("size-8", "bg-muted");
		expect(mark).toHaveClass("size-6", "object-contain");
	});

	it("keeps the provider name available when the mark is not decorative", () => {
		render(<HarnessLogo decorative={false} provider="gemini" />);

		expect(screen.getByRole("img", { name: "gemini" })).toBeInTheDocument();
	});

	it("does not expose a duplicate accessible image by default", () => {
		render(<HarnessLogo provider="claude-code" />);

		expect(screen.queryByRole("img")).not.toBeInTheDocument();
	});

	it("uses an explicit blend mapping only for opaque monochrome marks", () => {
		const { container, rerender } = render(<HarnessLogo decorative={false} provider="grok" />);
		expect(container.querySelector("img")).toHaveClass("mix-blend-multiply", "dark:mix-blend-screen", "dark:invert");

		rerender(<HarnessLogo decorative={false} provider="aider" />);
		expect(container.querySelector("img")).not.toHaveClass("mix-blend-multiply", "mix-blend-screen", "invert");
	});

	it("falls back to the provider initial for an unknown provider", () => {
		render(<HarnessLogo decorative={false} provider="new-harness" />);

		expect(screen.getByRole("img", { name: "new-harness" })).toHaveTextContent("N");
	});
});
