import { act, render } from "@testing-library/react";
import { useRef, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { SwipeNavigation } from "./SwipeNavigation";
import { useUiStore } from "../stores/ui-store";
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
		useUiStore.setState({ navigationGestures: true });
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

	it("ignores back and forward swipes when navigation gestures are disabled", () => {
		useUiStore.setState({ navigationGestures: false });
		const { getByTestId } = render(<Harness />);
		const pane = getByTestId("pane");
		swipe(pane, drag(-20, 8));
		swipe(pane, drag(20, 8));
		expect(history.back).not.toHaveBeenCalled();
		expect(history.forward).not.toHaveBeenCalled();
		expect(pane.querySelector("[data-swipe-puck]")).toBeNull();
		expect(phaseListeners.size).toBe(0);
	});

	it("cancels a pending wheel swipe when disabled and navigates again after enabling", () => {
		const { getByTestId } = render(<Harness />);
		const pane = getByTestId("pane");
		swipe(pane, drag(-20, 8), 0);
		act(() => useUiStore.getState().setNavigationGestures(false));
		act(() => vi.advanceTimersByTime(200));
		expect(history.back).not.toHaveBeenCalled();
		act(() => useUiStore.getState().setNavigationGestures(true));
		swipe(pane, drag(-20, 8));
		expect(history.back).toHaveBeenCalledTimes(1);
	});

	it("cancels a held native swipe when disabled", () => {
		const { getByTestId } = render(<Harness />);
		fingers("down");
		swipe(getByTestId("pane"), drag(-20, 8));
		act(() => useUiStore.getState().setNavigationGestures(false));
		fingers("up");
		expect(history.back).not.toHaveBeenCalled();
		expect(phaseListeners.size).toBe(0);
	});

	it("snaps back without navigating when the swipe is short", () => {
		const { getByTestId } = render(<Harness />);
		swipe(getByTestId("pane"), drag(-20, 3));
		expect(history.back).not.toHaveBeenCalled();
	});

	describe("with native finger phases", () => {
		it("goes back as soon as a fast swipe decays into momentum, before the scroll ends", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, [...drag(-30, 5), [-27, 0], [-24, 0], [-21, 0], [-18, 0]], 0);
			expect(history.back).toHaveBeenCalledTimes(1);
			swipe(pane, [[-15, 0], [-12, 0], [-9, 0], [-6, 0], [-3, 0]], 0);
			fingers("up");
			expect(history.back).toHaveBeenCalledTimes(1);
		});

		it("keeps a slow swipe armed while it eases off, until the fingers lift", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, [...drag(-8, 16), [-7, 0], [-6, 0], [-5, 0], [-4, 0], [-3, 0]], 500);
			expect(history.back).not.toHaveBeenCalled();
			fingers("up");
			expect(history.back).toHaveBeenCalledTimes(1);
		});

		it("ignores momentum that arrives as a new scroll right after the lift", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, drag(-20, 3), 0);
			fingers("up");
			fingers("down");
			swipe(pane, [-30, -27, -24, -21, -18, -15, -12, -9, -6, -3].map((deltaX) => [deltaX, 0] as [number, number]), 0);
			fingers("up");
			expect(history.back).not.toHaveBeenCalled();
		});

		it("starts a new swipe right after the previous scroll ends", () => {
			const { getByTestId } = render(<Harness />);
			const pane = getByTestId("pane");
			fingers("down");
			swipe(pane, drag(-20, 3), 0);
			fingers("up");
			fingers("down");
			swipe(pane, [[-4, 0], [-8, 0], [-12, 0], ...drag(-16, 8)], 0);
			fingers("up");
			expect(history.back).toHaveBeenCalledTimes(1);
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

	it("leaves the swipe to a scroller inside an open shadow root", () => {
		const { getByTestId } = render(<Harness><div data-testid="host" /></Harness>);
		const root = getByTestId("host").attachShadow({ mode: "open" });
		const scroller = document.createElement("div");
		scroller.style.overflowX = "auto";
		const cell = document.createElement("span");
		scroller.append(cell);
		root.append(scroller);
		Object.defineProperties(scroller, {
			scrollWidth: { value: 800 },
			clientWidth: { value: 400 },
			scrollLeft: { value: 120, writable: true },
		});
		const wheel = (deltaX: number) => new WheelEvent("wheel", { bubbles: true, composed: true, deltaX, deltaMode: 0 });
		act(() => {
			cell.dispatchEvent(wheel(-20));
			// The browser scrolls the code view with the swipe.
			scroller.scrollLeft = 100;
		});
		act(() => {
			for (let i = 0; i < 7; i++) {
				cell.dispatchEvent(wheel(-20));
				vi.advanceTimersByTime(8);
			}
			vi.advanceTimersByTime(200);
		});
		expect(history.back).not.toHaveBeenCalled();
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
