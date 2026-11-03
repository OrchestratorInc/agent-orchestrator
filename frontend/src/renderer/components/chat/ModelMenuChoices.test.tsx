import { fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it } from "vitest";
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

	const renderRows = (models: { id: string; label: string }[], counts?: Map<string, number[]>) =>
		render(
			<ModelMenuChoices models={models} searchable>
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
});
