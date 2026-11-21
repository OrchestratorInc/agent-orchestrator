import { act, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, onTestFinished, vi } from "vitest";
import { ModelMenuChoices } from "./ModelMenuChoices";

describe("ModelMenuChoices", () => {
	const generateModels = (count: number) =>
		Array.from({ length: count }, (_, index) => ({
			id: `provider/model-${index}`,
			label: `Model ${index}`,
		}));

	it("limits initially rendered items for large catalogs to prevent menu lag", () => {
		const models = generateModels(500);
		render(
			<ModelMenuChoices models={models}>
				{(matches) => (
					<div data-testid="results">
						{matches.map((model) => (
							<div key={model.id} role="menuitemradio">
								{model.label}
							</div>
						))}
					</div>
				)}
			</ModelMenuChoices>,
		);

		const items = screen.getAllByRole("menuitemradio");
		expect(items).toHaveLength(100);
		expect(items[0]).toHaveTextContent("Model 0");
		expect(items[99]).toHaveTextContent("Model 99");
	});

	it("ensures a selected model beyond the default threshold is included in initial visible items", () => {
		const models = generateModels(300);
		render(
			<ModelMenuChoices models={models} selectedId="provider/model-120">
				{(matches) => (
					<div data-testid="results">
						{matches.map((model) => (
							<div key={model.id} role="menuitemradio">
								{model.label}
							</div>
						))}
					</div>
				)}
			</ModelMenuChoices>,
		);

		const items = screen.getAllByRole("menuitemradio");
		expect(items.length).toBeGreaterThanOrEqual(130);
		expect(screen.getByText("Model 120")).toBeInTheDocument();
	});

	it("loads more items progressively on scroll", () => {
		const models = generateModels(500);
		const { container } = render(
			<ModelMenuChoices models={models}>
				{(matches) => (
					<div data-testid="results">
						{matches.map((model) => (
							<div key={model.id} role="menuitemradio">
								{model.label}
							</div>
						))}
					</div>
				)}
			</ModelMenuChoices>,
		);

		expect(screen.getAllByRole("menuitemradio")).toHaveLength(100);

		const scrollContainer = container.querySelector(".model-menu-scroll");
		expect(scrollContainer).not.toBeNull();

		// Simulate scrolling near the bottom
		Object.defineProperty(scrollContainer, "scrollHeight", { configurable: true, value: 1000 });
		Object.defineProperty(scrollContainer, "scrollTop", { configurable: true, value: 700 });
		Object.defineProperty(scrollContainer, "clientHeight", { configurable: true, value: 200 });

		fireEvent.scroll(scrollContainer!);

		expect(screen.getAllByRole("menuitemradio")).toHaveLength(150);
	});

	it("filters items by query and limits search results to 50", async () => {
		const user = userEvent.setup();
		const models = generateModels(200);
		render(
			<ModelMenuChoices models={models} searchable>
				{(matches) => (
					<div data-testid="results">
						{matches.map((model) => (
							<div key={model.id} role="menuitemradio">
								{model.label}
							</div>
						))}
					</div>
				)}
			</ModelMenuChoices>,
		);

		const searchInput = screen.getByRole("searchbox");
		await user.type(searchInput, "Model 1");

		const items = screen.getAllByRole("menuitemradio");
		// Query "Model 1" matches 1, 10-19, 100-199 (over 100 matches), capped at 50 for search
		expect(items).toHaveLength(50);
	});

	const renderRows = (models: { id: string; label: string }[], counts?: Map<string, number[]>, rows?: number) =>
		render(
			<ModelMenuChoices models={models} searchable rows={rows}>
				{(matches, _active, _optionID, query) => {
					if (query) counts?.set(query, [...(counts.get(query) ?? []), matches.length]);
					return matches.map((model) => (
						<button key={model.id} type="button" role="menuitemradio">
							{model.label}
						</button>
					));
				}}
			</ModelMenuChoices>,
		);

	const scrollToBottom = (element: Element) => {
		Object.defineProperty(element, "scrollHeight", { configurable: true, value: 1000 });
		Object.defineProperty(element, "scrollTop", { configurable: true, writable: true, value: 700 });
		Object.defineProperty(element, "clientHeight", { configurable: true, value: 200 });
	};

	it("adds one batch for several scroll events before the list grows", () => {
		const { container } = renderRows(generateModels(500));
		const scroller = container.querySelector(".model-menu-scroll")!;
		scrollToBottom(scroller);
		fireEvent.scroll(scroller);
		fireEvent.scroll(scroller);
		fireEvent.scroll(scroller);
		// Each commit grows the list by one batch from what is rendered.
		expect(screen.getAllByRole("menuitemradio").length).toBeLessThanOrEqual(250);
	});

	it("starts a new query at the search cap without committing the longer list first", async () => {
		const user = userEvent.setup();
		const counts = new Map<string, number[]>();
		const { container } = renderRows(generateModels(500), counts);
		const scroller = container.querySelector(".model-menu-scroll")!;
		scrollToBottom(scroller);
		fireEvent.scroll(scroller);
		fireEvent.scroll(scroller);
		expect(screen.getAllByRole("menuitemradio").length).toBeGreaterThan(100);

		await user.type(screen.getByRole("searchbox"), "M");
		expect(Math.max(...counts.get("m")!)).toBe(50);

		// Loading more in one query does not carry over to the next.
		scrollToBottom(scroller);
		fireEvent.scroll(scroller);
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(100);
		await user.type(screen.getByRole("searchbox"), "o");
		expect(Math.max(...counts.get("mo")!)).toBe(50);
	});

	it("loads more on ArrowDown only near the last rendered row", () => {
		renderRows(generateModels(500));
		const rows = screen.getAllByRole("menuitemradio");
		fireEvent.keyDown(rows[0], { key: "ArrowDown" });
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(100);
		fireEvent.keyDown(rows[95], { key: "ArrowDown" });
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(150);
	});

	it("fixes the list at the requested number of rows, so a search with no matches cannot shrink it", () => {
		renderRows(generateModels(30), undefined, 3);
		const pane = document.querySelector<HTMLElement>(".model-menu-scroll")!.parentElement!;
		const height = "calc((var(--line-height-row) + var(--space-1_5) * 2) * 3 + 1px * 3)";
		// Its basis, not its content: a pane sized by its content is a single line once nothing
		// matches, and the menu drops out from under the pointer.
		expect(pane.style.getPropertyValue("--model-list-rows")).toBe(height);
		expect(pane).toHaveClass("basis-(--model-list-rows)");
		// Every row still renders; the list scrolls under the cap.
		expect(screen.getAllByRole("menuitemradio")).toHaveLength(30);
	});

	it("keeps two rows of the list in a window too short for all of it", () => {
		renderRows(generateModels(30), undefined, 4);
		const pane = document.querySelector<HTMLElement>(".model-menu-scroll")!.parentElement!;
		// Past this the menu scrolls as a whole, rather than closing the list up to nothing.
		expect(pane.style.getPropertyValue("--model-list-min")).toBe("calc((var(--line-height-row) + var(--space-1_5) * 2) * 2)");
		expect(pane).toHaveClass("min-h-(--model-list-min)");
	});

	it("keeps the footer, and with it the menu's height, while searching a fixed-height list", async () => {
		const user = userEvent.setup();
		render(
			<ModelMenuChoices models={generateModels(5)} searchable rows={3} footer={<div data-testid="footer" />}>
				{(matches) => matches.map((model) => (
					<button key={model.id} type="button" role="menuitemradio">{model.label}</button>
				))}
			</ModelMenuChoices>,
		);
		expect(screen.getByTestId("footer")).toBeInTheDocument();
		await user.type(screen.getByRole("searchbox"), "zzz");
		expect(screen.getByText("No matching models.")).toBeInTheDocument();
		// A capped list gains no room from hiding the footer, so the menu would only lose a row.
		expect(screen.getByTestId("footer")).toBeInTheDocument();
	});

	it("hides the header while searching and gives the list a row more in its place", async () => {
		const user = userEvent.setup();
		render(
			<ModelMenuChoices models={generateModels(30)} searchable rows={4} header={<div data-testid="rail" />}>
				{(matches) => matches.map((model) => (
					<button key={model.id} type="button" role="menuitemradio">{model.label}</button>
				))}
			</ModelMenuChoices>,
		);
		const pane = () => document.querySelector<HTMLElement>(".model-menu-scroll")!.parentElement!;
		expect(screen.getByTestId("rail")).toBeInTheDocument();
		expect(pane().style.getPropertyValue("--model-list-rows")).toBe("calc((var(--line-height-row) + var(--space-1_5) * 2) * 4 + 1px * 4)");
		await user.type(screen.getByRole("searchbox"), "Model 1");
		expect(screen.queryByTestId("rail")).not.toBeInTheDocument();
		expect(pane().style.getPropertyValue("--model-list-rows")).toBe("calc((var(--line-height-row) + var(--space-1_5) * 2) * 5 + 1px * 5)");
		await user.clear(screen.getByRole("searchbox"));
		expect(screen.getByTestId("rail")).toBeInTheDocument();
	});

	it("passes over matches that cannot be picked when Enter picks from the search", async () => {
		const user = userEvent.setup();
		const picked = vi.fn();
		const pickable = (model: { id: string }) => model.id !== "provider/model-1";
		render(
			<ModelMenuChoices models={generateModels(20)} searchable pickable={pickable}>
				{(matches, searchActiveID, optionID) => matches.map((model) => (
					<button
						key={model.id}
						id={optionID?.(model.id)}
						type="button"
						role="menuitemradio"
						data-disabled={pickable(model) ? undefined : ""}
						data-search-active={model.id === searchActiveID || undefined}
						onClick={() => picked(model.id)}
					>
						{model.label}
					</button>
				))}
			</ModelMenuChoices>,
		);
		await user.type(screen.getByRole("searchbox"), "Model 1");
		// "Model 1" is the first match, and cannot be picked; "Model 10" is the first that can.
		expect(screen.getByRole("menuitemradio", { name: "Model 10" })).toHaveAttribute("data-search-active");
		await user.keyboard("{Enter}");
		expect(picked).toHaveBeenCalledExactlyOnceWith("provider/model-10");
	});

	it("hides the footer while searching a list that grows with its results", async () => {
		const user = userEvent.setup();
		render(
			<ModelMenuChoices models={generateModels(5)} searchable footer={<div data-testid="footer" />}>
				{(matches) => matches.map((model) => (
					<button key={model.id} type="button" role="menuitemradio">{model.label}</button>
				))}
			</ModelMenuChoices>,
		);
		expect(screen.getByTestId("footer")).toBeInTheDocument();
		await user.type(screen.getByRole("searchbox"), "M");
		expect(screen.queryByTestId("footer")).not.toBeInTheDocument();
	});

	it("takes the pane's own max height when no row cap is asked for", () => {
		renderRows(generateModels(30));
		const pane = document.querySelector<HTMLElement>(".model-menu-scroll")!.parentElement!;
		expect(pane.style.getPropertyValue("--model-list-rows")).toBe("");
		expect(pane).toHaveClass("flex-1");
	});

	it("holds the tallest height it reached while open, so shorter settings rows leave the room to the list", () => {
		// Only the observer watching the menu's root; the list's edge fade has its own.
		const watching = new Map<Element, () => void>();
		const original = window.ResizeObserver;
		window.ResizeObserver = class {
			callback: () => void;
			constructor(callback: () => void) { this.callback = callback; }
			observe(target: Element) { watching.set(target, this.callback); }
			unobserve() {}
			disconnect() {}
		} as unknown as typeof ResizeObserver;
		onTestFinished(() => { window.ResizeObserver = original; });
		let height = 240;
		const offsetHeight = vi.spyOn(HTMLElement.prototype, "offsetHeight", "get").mockImplementation(() => height);
		onTestFinished(() => offsetHeight.mockRestore());
		const { container } = renderRows(generateModels(5), undefined, 3);
		const root = container.firstElementChild as HTMLElement;
		const notify = () => watching.get(root)?.();
		act(() => notify());
		expect(root.style.minHeight).toBe("240px");
		// Another agent's footer is one row shorter: the menu keeps its height.
		height = 204;
		act(() => notify());
		expect(root.style.minHeight).toBe("240px");
		// A taller one grows it, and that becomes the height held.
		height = 276;
		act(() => notify());
		expect(root.style.minHeight).toBe("276px");
	});
});
