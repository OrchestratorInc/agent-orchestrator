import { cleanup, render } from "@testing-library/react";
import { useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTitleMarquee } from "./useTitleMarquee";

type Callback = (entries: ResizeObserverEntry[]) => void;
let fire: Callback = () => {};
const observed: Element[] = [];

class FakeResizeObserver {
	constructor(callback: Callback) {
		fire = callback;
	}
	observe(target: Element) {
		observed.push(target);
	}
	unobserve(target: Element) {
		observed.splice(observed.indexOf(target), 1);
	}
	disconnect() {}
}

function entry(target: Element, width: number): ResizeObserverEntry {
	const size = [{ inlineSize: width, blockSize: 20 }];
	return { target, borderBoxSize: size, contentBoxSize: size } as unknown as ResizeObserverEntry;
}

function Title() {
	const box = useRef<HTMLSpanElement>(null);
	const text = useRef<HTMLSpanElement>(null);
	useTitleMarquee(box, text);
	return (
		<span data-testid="box" ref={box}>
			<span data-testid="text" ref={text}>A long session title</span>
		</span>
	);
}

describe("useTitleMarquee", () => {
	beforeEach(() => {
		vi.stubGlobal("ResizeObserver", FakeResizeObserver);
	});
	afterEach(() => {
		cleanup();
		vi.unstubAllGlobals();
	});

	it("marks a title that overflows with how far and how long to scroll", () => {
		const { getByTestId } = render(<Title />);
		const box = getByTestId("box");
		fire([entry(box, 132), entry(getByTestId("text"), 292)]);

		expect(box).toHaveAttribute("data-overflow");
		expect(box.style.getPropertyValue("--marquee-distance")).toBe("160px");
		expect(box.style.getPropertyValue("--marquee-duration")).toBe("5.26s");
		// The first movement lands at the same time for every length.
		expect(box.style.getPropertyValue("--marquee-delay")).toBe("0.67s");
	});

	it("clears the marks once the title fits and stops observing on unmount", () => {
		const { getByTestId, unmount } = render(<Title />);
		const box = getByTestId("box");
		const text = getByTestId("text");
		fire([entry(box, 132), entry(text, 292)]);
		fire([entry(box, 320)]);

		expect(box).not.toHaveAttribute("data-overflow");
		expect(box.style.getPropertyValue("--marquee-distance")).toBe("");

		unmount();
		expect(observed).not.toContain(box);
		expect(observed).not.toContain(text);
	});
});
