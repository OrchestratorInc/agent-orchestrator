import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { PRTabChip } from "./PRTabPane";

function renderChip(active: boolean) {
	const onActivate = vi.fn();
	const onClose = vi.fn();
	render(
		<QueryClientProvider client={new QueryClient()}>
			<div role="tablist">
				<PRTabChip active={active} onActivate={onActivate} onClose={onClose} url="https://github.com/o/r/pull/1" />
			</div>
		</QueryClientProvider>,
	);
	return { onActivate, onClose };
}

describe("PRTabChip", () => {
	it("keeps the state icon and shows close over the title's end", () => {
		renderChip(true);
		const tab = screen.getByRole("tab", { name: "Loading pull request…" });
		const slot = screen.getByRole("button", { name: "Close Loading pull request…" }).closest("[data-terminal-tab-action]");
		expect(slot?.previousElementSibling).toBe(tab);
		expect(slot).toHaveClass("absolute", "right-2");
		expect(slot).not.toHaveClass("opacity-0");
		expect(tab.querySelector("svg")?.parentElement).not.toHaveClass("group-hover:opacity-0");
	});

	it("shows an unselected tab's close on hover, and closes without selecting", () => {
		const { onActivate, onClose } = renderChip(false);
		const close = screen.getByRole("button", { name: "Close Loading pull request…" });
		expect(close.closest("[data-terminal-tab-action]")).toHaveClass("opacity-0", "group-hover:opacity-100");
		fireEvent.click(close);
		expect(onClose).toHaveBeenCalledOnce();
		expect(onActivate).not.toHaveBeenCalled();
	});

	it("closes on a middle-click", () => {
		const { onClose } = renderChip(false);
		const frame = screen.getByRole("tab", { name: "Loading pull request…" }).closest("[data-terminal-tab-frame]")!;
		fireEvent(frame, new MouseEvent("auxclick", { bubbles: true, button: 1 }));
		expect(onClose).toHaveBeenCalledOnce();
	});
});
