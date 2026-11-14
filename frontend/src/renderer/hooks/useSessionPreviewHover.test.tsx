import { act, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { inTriangle, resetSessionPreviewWarmth, safeTriangle, useSessionPreviewHover } from "./useSessionPreviewHover";

const CARD = { left: 200, right: 400, top: 0, bottom: 200 };

function Row({ id }: { id: string }) {
	const preview = useSessionPreviewHover(false);
	return (
		<>
			<div data-testid={`row-${id}`} ref={preview.rowRef} {...preview.triggerProps} />
			{preview.open ? <div data-testid={`card-${id}`} {...preview.contentProps} /> : null}
		</>
	);
}

function rect({ left, right, top, bottom }: typeof CARD) {
	return { left, right, top, bottom, x: left, y: top, width: right - left, height: bottom - top, toJSON() {} } as DOMRect;
}

const move = (x: number, y: number) => fireEvent.pointerMove(window, { clientX: x, clientY: y });
const wait = (ms: number) => act(() => vi.advanceTimersByTime(ms));

function openFirstRow() {
	render(
		<>
			<Row id="a" />
			<Row id="b" />
		</>,
	);
	fireEvent.pointerEnter(screen.getByTestId("row-a"), { clientX: 100, clientY: 40 });
	wait(200);
	expect(screen.getByTestId("card-a")).toBeInTheDocument();
}

describe("safeTriangle", () => {
	it("aims at the card edge facing the exit point, wherever the card sits", () => {
		const right = safeTriangle({ x: 180, y: 40 }, CARD);
		expect(right?.slice(1)).toEqual([
			{ x: 200, y: 0 },
			{ x: 200, y: 200 },
		]);
		const left = safeTriangle({ x: 450, y: 40 }, CARD);
		expect(left?.slice(1)).toEqual([
			{ x: 400, y: 0 },
			{ x: 400, y: 200 },
		]);
		expect(safeTriangle({ x: 300, y: 260 }, CARD)?.slice(1)).toEqual([
			{ x: 200, y: 200 },
			{ x: 400, y: 200 },
		]);
		expect(safeTriangle({ x: 300, y: -40 }, CARD)?.slice(1)).toEqual([
			{ x: 200, y: 0 },
			{ x: 400, y: 0 },
		]);
		expect(safeTriangle({ x: 300, y: 100 }, CARD)).toBeUndefined();
	});

	it("contains a diagonal path to the card and nothing behind the exit point", () => {
		const triangle = safeTriangle({ x: 180, y: 40 }, CARD);
		if (!triangle) throw new Error("expected a triangle");
		expect(inTriangle({ x: 181, y: 41 }, triangle)).toBe(true);
		expect(inTriangle({ x: 190, y: 90 }, triangle)).toBe(true);
		expect(inTriangle({ x: 150, y: 60 }, triangle)).toBe(false);
		expect(inTriangle({ x: 185, y: 150 }, triangle)).toBe(false);
	});
});

describe("useSessionPreviewHover safe triangle", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		resetSessionPreviewWarmth();
		const original = HTMLElement.prototype.getBoundingClientRect;
		vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function (this: HTMLElement) {
			return this.dataset.testid?.startsWith("card-") ? rect(CARD) : original.call(this);
		});
	});

	afterEach(() => {
		resetSessionPreviewWarmth();
		vi.restoreAllMocks();
		vi.useRealTimers();
	});

	it("keeps the card open across other rows while the pointer heads for it", () => {
		openFirstRow();
		fireEvent.pointerLeave(screen.getByTestId("row-a"), { clientX: 180, clientY: 40 });
		fireEvent.pointerEnter(screen.getByTestId("row-b"), { clientX: 185, clientY: 50 });
		move(185, 50);
		wait(250);
		move(192, 70);
		wait(250);
		expect(screen.getByTestId("card-a")).toBeInTheDocument();
		expect(screen.queryByTestId("card-b")).not.toBeInTheDocument();

		fireEvent.pointerEnter(screen.getByTestId("card-a"));
		move(210, 70);
		wait(1000);
		expect(screen.getByTestId("card-a")).toBeInTheDocument();
		fireEvent.pointerLeave(screen.getByTestId("card-a"));
		wait(100);
		expect(screen.queryByTestId("card-a")).not.toBeInTheDocument();
	});

	it("hands over to the row under the pointer when it stalls in the triangle", () => {
		openFirstRow();
		fireEvent.pointerLeave(screen.getByTestId("row-a"), { clientX: 180, clientY: 40 });
		fireEvent.pointerEnter(screen.getByTestId("row-b"), { clientX: 185, clientY: 50 });
		move(185, 50);
		wait(200);
		expect(screen.getByTestId("card-a")).toBeInTheDocument();
		wait(150);
		expect(screen.queryByTestId("card-a")).not.toBeInTheDocument();
		expect(screen.getByTestId("card-b")).toBeInTheDocument();
	});

	it("switches rows at once when the pointer leaves the triangle", () => {
		openFirstRow();
		fireEvent.pointerLeave(screen.getByTestId("row-a"), { clientX: 120, clientY: 40 });
		fireEvent.pointerEnter(screen.getByTestId("row-b"), { clientX: 100, clientY: 60 });
		move(100, 60);
		expect(screen.queryByTestId("card-a")).not.toBeInTheDocument();
		expect(screen.getByTestId("card-b")).toBeInTheDocument();
	});

	it("closes with the normal delay when the pointer leaves away from the card", () => {
		openFirstRow();
		fireEvent.pointerLeave(screen.getByTestId("row-a"), { clientX: -1, clientY: 40 });
		move(-10, 40);
		expect(screen.getByTestId("card-a")).toBeInTheDocument();
		wait(100);
		expect(screen.queryByTestId("card-a")).not.toBeInTheDocument();
	});

	it("keeps the card when the pointer comes back to its row", () => {
		openFirstRow();
		fireEvent.pointerLeave(screen.getByTestId("row-a"), { clientX: 180, clientY: 40 });
		move(182, 42);
		fireEvent.pointerEnter(screen.getByTestId("row-a"), { clientX: 179, clientY: 40 });
		wait(1000);
		expect(screen.getByTestId("card-a")).toBeInTheDocument();
	});
});
