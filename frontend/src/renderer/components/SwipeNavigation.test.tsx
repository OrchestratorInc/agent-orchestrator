import { act, render } from "@testing-library/react";
import { useRef, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SwipeNavigation } from "./SwipeNavigation";
import { useCanGoForward } from "./TitlebarNav";
import type { ScrollPhase } from "../../shared/scroll-phase";

type HistoryListener = (event: { location: { state: { __TSR_index: number } }; action: { type: string } }) => void;

const { history, listeners, phaseListeners } = vi.hoisted(() => {
	const listeners: HistoryListener[] = [];
	return {
		listeners,
		phaseListeners: new Set<(phase: ScrollPhase) => void>(),
		history: {
			back: vi.fn(),
			forward: vi.fn(),
			canGoBack: vi.fn(() => true),
			location: { state: { __TSR_index: 0 } },
			subscribe: vi.fn((listener: HistoryListener) => {
				listeners.push(listener);
				return () => undefined;
			}),
		},
	};
});

vi.mock("@tanstack/react-router", () => ({
	useCanGoBack: () => true,
	useRouter: () => ({ history }),
}));

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		app: {
			onScrollPhase: (listener: (phase: ScrollPhase) => void) => {
				phaseListeners.add(listener);
				return () => phaseListeners.delete(listener);
			},
		},
	},
}));

function fingers(phase: ScrollPhase) {
	act(() => {
		for (const listener of phaseListeners) listener(phase);
	});
}

function Harness({ backLocked = false, children }: { backLocked?: boolean; children?: ReactNode }) {
	const ref = useRef<HTMLDivElement>(null);
	const canGoForward = useCanGoForward();
	return (
		<div data-testid="pane" ref={ref}>
			{children}
			<SwipeNavigation containerRef={ref} navigation={{ canGoBack: !backLocked && history.canGoBack(), canGoForward, back: history.back, forward: history.forward }} />
		</div>
	);
}

function swipe(target: Element, deltas: Array<[number, number]>, idleAfter = 200) {
	act(() => {
		for (const [deltaX, deltaY] of deltas) {
			target.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaX, deltaY, deltaMode: 0 }));
			vi.advanceTimersByTime(8);
		}
		vi.advanceTimersByTime(idleAfter);
	});
}

const drag = (deltaX: number, count: number) => Array.from({ length: count }, () => [deltaX, 0] as [number, number]);

describe("SwipeNavigation", () => {
	beforeEach(() => {
		vi.useFakeTimers();
		history.canGoBack.mockReturnValue(true);
	});

	afterEach(() => {
		vi.useRealTimers();
		vi.clearAllMocks();
		listeners.length = 0;
		phaseListeners.clear();
	});

	it("goes back after a two-finger swipe right past the threshold", () => {
		const { getByTestId } = render(<Harness />);
		swipe(getByTestId("pane"), drag(-20, 8));
		expect(history.back).toHaveBeenCalledTimes(1);
		expect(history.forward).not.toHaveBeenCalled();
	});

	it("snaps back without navigating when the swipe is short", () => {
		const { getByTestId } = render(<Harness />);
		swipe(getByTestId("pane"), drag(-20, 3));
		expect(history.back).not.toHaveBeenCalled();
	});

	describe("with native finger phases", () => {
		it("discards an armed gesture when macOS cancels it", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, drag(-20, 8));
			fingers("cancel");
			swipe(pane, drag(-20, 12));
			expect(history.back).not.toHaveBeenCalled();
			expect(pane.querySelector('[data-swipe-puck="back"]')).toHaveStyle({ opacity: "0" });
		});
		it("holds an armed swipe until the fingers lift, then goes back", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, drag(-20, 8), 2000);
			expect(pane.querySelector('[data-swipe-puck="back"]')).toHaveAttribute("data-armed");
			expect(history.back).not.toHaveBeenCalled();
			fingers("up");
			expect(history.back).toHaveBeenCalledTimes(1);
		});

		it("cancels when the swipe is pulled back before lifting", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, drag(-20, 8));
			swipe(pane, drag(20, 4));
			fingers("up");
			expect(history.back).not.toHaveBeenCalled();
		});

		it("ignores momentum after the lift", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, drag(-20, 3));
			fingers("up");
			swipe(pane, drag(-20, 20));
			expect(history.back).not.toHaveBeenCalled();
		});
	});

	it("ignores vertical scrolling", () => {
		const { getByTestId } = render(<Harness />);
		swipe(getByTestId("pane"), [[-3, 20], ...drag(-20, 10)]);
		expect(history.back).not.toHaveBeenCalled();
	});

	it("does not go back from the home route or without back history", () => {
		const { getByTestId, rerender } = render(<Harness backLocked />);
		swipe(getByTestId("pane"), drag(-20, 8));
		history.canGoBack.mockReturnValue(false);
		rerender(<Harness />);
		swipe(getByTestId("pane"), drag(-20, 8));
		expect(history.back).not.toHaveBeenCalled();
	});

	it("still goes forward from the home route", () => {
		const { getByTestId } = render(<Harness backLocked />);
		act(() => {
			for (const listener of listeners) {
				listener({ location: { state: { __TSR_index: 1 } }, action: { type: "PUSH" } });
				listener({ location: { state: { __TSR_index: 0 } }, action: { type: "BACK" } });
			}
		});
		swipe(getByTestId("pane"), drag(20, 8));
		expect(history.forward).toHaveBeenCalledTimes(1);
	});

	it("leaves the swipe to content that can still scroll horizontally", () => {
		const { getByTestId } = render(
			<Harness>
				<div data-testid="scroller" style={{ overflowX: "auto" }}>
					<span data-testid="cell">wide</span>
				</div>
			</Harness>,
		);
		const scroller = getByTestId("scroller");
		Object.defineProperties(scroller, {
			scrollWidth: { value: 800 },
			clientWidth: { value: 400 },
			scrollLeft: { value: 120, writable: true },
		});
		const cell = getByTestId("cell");
		act(() => {
			cell.dispatchEvent(new WheelEvent("wheel", { bubbles: true, deltaX: -20, deltaMode: 0 }));
			// The browser scrolls the content with the swipe.
			scroller.scrollLeft = 100;
		});
		swipe(cell, drag(-20, 8));
		expect(history.back).not.toHaveBeenCalled();

		// Already at its left edge, the content stays put and the swipe navigates.
		scroller.scrollLeft = 0;
		swipe(cell, drag(-20, 8));
		expect(history.back).toHaveBeenCalledTimes(1);
	});

	it("goes forward on a swipe left only when forward history exists", () => {
		const { getByTestId } = render(<Harness />);
		swipe(getByTestId("pane"), drag(20, 8));
		expect(history.forward).not.toHaveBeenCalled();

		act(() => {
			for (const listener of listeners) {
				listener({ location: { state: { __TSR_index: 1 } }, action: { type: "PUSH" } });
				listener({ location: { state: { __TSR_index: 0 } }, action: { type: "BACK" } });
			}
		});
		swipe(getByTestId("pane"), drag(20, 8));
		expect(history.forward).toHaveBeenCalledTimes(1);
	});

	it("does not navigate when a single large event scrolls content before release", () => {
		const { getByTestId } = render(<Harness><div data-testid="scroller" style={{ overflowX: "auto" }} /></Harness>);
		const scroller = getByTestId("scroller");
		Object.defineProperties(scroller, {
			scrollWidth: { value: 800 }, clientWidth: { value: 400 }, scrollLeft: { value: 200, writable: true },
		});
		fingers("down");
		swipe(scroller, [[-150, 0]], 0);
		scroller.scrollLeft = 50;
		fingers("up");
		expect(history.back).not.toHaveBeenCalled();
	});
});
