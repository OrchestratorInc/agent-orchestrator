import { render, screen } from "@testing-library/react";
import { renderToStaticMarkup } from "react-dom/server";
import { expect, it } from "vitest";
import { LiveStatus } from "./LiveStatus";

// A live region that mounts together with its text is often not announced.
it("mounts empty and then says its text", () => {
	expect(renderToStaticMarkup(<LiveStatus text="Loading repositories..." />)).not.toContain("Loading repositories...");
	render(<LiveStatus text="Loading repositories..." />);
	expect(screen.getByRole("status")).toHaveTextContent("Loading repositories...");
});

it("stays mounted and swaps its text", () => {
	const view = render(<LiveStatus text="" />);
	const status = screen.getByRole("status");
	view.rerender(<LiveStatus text="Loading repositories..." />);
	expect(screen.getByRole("status")).toBe(status);
	expect(status).toHaveTextContent("Loading repositories...");
	view.rerender(<LiveStatus text="" />);
	expect(status).toBeEmptyDOMElement();
});
