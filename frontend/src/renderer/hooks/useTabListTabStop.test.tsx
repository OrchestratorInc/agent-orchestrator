import { render } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useTabListTabStop } from "./useTabListTabStop";

function Tabs({ active, count = 3 }: { active: number | null; count?: number }) {
	const ref = useTabListTabStop<HTMLDivElement>();
	return (
		<div ref={ref} role="tablist">
			{Array.from({ length: count }, (_, i) => (
				<button key={i} role="tab" aria-selected={active === i} tabIndex={active === i ? 0 : -1}>
					tab {i}
				</button>
			))}
		</div>
	);
}

const tabStops = (container: HTMLElement) => Array.from(container.querySelectorAll<HTMLElement>('[role="tab"]')).filter((tab) => tab.tabIndex === 0);

describe("useTabListTabStop", () => {
	afterEach(() => vi.restoreAllMocks());

	it("makes the first tab tabbable when none is active", () => {
		const { container } = render(<Tabs active={null} />);
		const stops = tabStops(container);
		expect(stops).toHaveLength(1);
		expect(stops[0]).toHaveTextContent("tab 0");
	});

	it("leaves the active tab as the only tab stop", () => {
		const { container } = render(<Tabs active={1} />);
		expect(tabStops(container).map((tab) => tab.textContent)).toEqual(["tab 1"]);
	});

	it("does not leave a second tab stop behind after the active tab appears", () => {
		const { container, rerender } = render(<Tabs active={null} />);
		expect(tabStops(container)).toHaveLength(1);
		rerender(<Tabs active={2} />);
		expect(tabStops(container).map((tab) => tab.textContent)).toEqual(["tab 2"]);
		rerender(<Tabs active={null} />);
		expect(tabStops(container).map((tab) => tab.textContent)).toEqual(["tab 0"]);
	});

	it("does nothing for an empty list", () => {
		const { container } = render(<Tabs active={null} count={0} />);
		expect(tabStops(container)).toHaveLength(0);
	});

	it("scrolls the selected tab into view only when the selection changes", () => {
		const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
		const { rerender } = render(<Tabs active={0} />);
		expect(scroll).toHaveBeenCalledTimes(1);
		rerender(<Tabs active={0} />);
		expect(scroll).toHaveBeenCalledTimes(1);
		rerender(<Tabs active={2} />);
		expect(scroll).toHaveBeenCalledTimes(2);
		expect(scroll.mock.contexts[1]).toHaveTextContent("tab 2");
	});

	it("does not scroll when no tab is selected", () => {
		const scroll = vi.spyOn(Element.prototype, "scrollIntoView");
		render(<Tabs active={null} />);
		expect(scroll).not.toHaveBeenCalled();
	});
});
