import { act, fireEvent, render, screen } from "@testing-library/react";
import { createRef } from "react";
import { describe, expect, it, vi } from "vitest";
import { INSPECTOR_SPRING_MS, SessionInspectorRail, inspectorSizing } from "./SessionInspectorRail";

vi.mock("motion/react", () => ({ useReducedMotion: () => false }));

describe("SessionInspectorRail session navigation", () => {
	const splitRef = createRef<HTMLDivElement>();
	const rail = (sessionKey: string, isOpen: boolean) => <SessionInspectorRail
		sessionKey={sessionKey}
		isOpen={isOpen}
		settledClosed={!isOpen}
		onExpand={() => {}}
		sizing={inspectorSizing("summary")}
		splitRef={splitRef}
	><input aria-label="Inspector draft" defaultValue="preserved" /></SessionInspectorRail>;

	it.each([[false, true], [true, false], [true, true], [false, false]])(
		"restores %s → %s across sessions without animating or remounting content",
		(from, to) => {
			const { rerender, container } = render(rail("first", from));
			const draft = screen.getByLabelText("Inspector draft");
			rerender(rail("second", to));
			expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-motion", "instant");
			expect(container.querySelector('[data-slot="inspector-gap"]')).toHaveAttribute("data-motion", "instant");
			expect(screen.getByLabelText("Inspector draft")).toBe(draft);
			expect(draft).toHaveValue("preserved");
			// Background updates must not re-enable the route transition.
			rerender(rail("second", to));
			expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-motion", "instant");
		},
	);

	it("animates explicit toggles in the current session, then snaps on navigation", () => {
		const { rerender, container } = render(rail("first", false));
		rerender(rail("first", true));
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-motion", "animated");
		expect(container.querySelector('[data-slot="inspector-gap"]')).toHaveAttribute("data-motion", "animated");
		rerender(rail("first", false));
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-motion", "animated");
		rerender(rail("second", true));
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-motion", "instant");
		rerender(rail("second", false));
		expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-motion", "animated");
	});

	it("settles a close from session navigation before paint", () => {
		const onClose = vi.fn();
		const closing = (sessionKey: string, isOpen: boolean) => <SessionInspectorRail
			sessionKey={sessionKey}
			isOpen={isOpen}
			settledClosed={false}
			onCloseAnimationComplete={onClose}
			onExpand={() => {}}
			sizing={inspectorSizing("summary")}
			splitRef={splitRef}
		>content</SessionInspectorRail>;
		const { rerender } = render(closing("first", true));
		rerender(closing("second", false));
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it("settles an in-session close when the panel's slide ends", () => {
		const onClose = vi.fn();
		const closing = (isOpen: boolean) => <SessionInspectorRail
			sessionKey="first"
			isOpen={isOpen}
			settledClosed={false}
			onCloseAnimationComplete={onClose}
			onExpand={() => {}}
			sizing={inspectorSizing("summary")}
			splitRef={splitRef}
		>content</SessionInspectorRail>;
		const { rerender } = render(closing(true));
		rerender(closing(false));
		expect(onClose).not.toHaveBeenCalled();
		const panel = screen.getByTestId("panel-inspector");
		const end = new Event("transitionend");
		Object.defineProperty(end, "propertyName", { value: "transform" });
		fireEvent(panel, end);
		expect(onClose).toHaveBeenCalledTimes(1);
	});

	it("settles an in-session close when its slide never reports an end", () => {
		vi.useFakeTimers();
		try {
			const onClose = vi.fn();
			const closing = (isOpen: boolean) => <SessionInspectorRail
				sessionKey="first"
				isOpen={isOpen}
				settledClosed={false}
				onCloseAnimationComplete={onClose}
				onExpand={() => {}}
				sizing={inspectorSizing("summary")}
				splitRef={splitRef}
			><span data-testid="inspector-child">content</span></SessionInspectorRail>;
			const { rerender } = render(closing(true));
			rerender(closing(false));
			// A child's transition is not the panel's slide.
			const childEnd = new Event("transitionend", { bubbles: true });
			Object.defineProperty(childEnd, "propertyName", { value: "transform" });
			fireEvent(screen.getByTestId("inspector-child"), childEnd);
			expect(onClose).not.toHaveBeenCalled();
			act(() => vi.advanceTimersByTime(INSPECTOR_SPRING_MS + 100));
			expect(onClose).toHaveBeenCalledTimes(1);
			expect(screen.getByTestId("panel-inspector")).toHaveAttribute("data-motion", "instant");
		} finally {
			vi.useRealTimers();
		}
	});
});
