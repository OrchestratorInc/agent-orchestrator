import { act, render } from "@testing-library/react";
import { visualElementStore, type VisualElement } from "motion/react";
import { Activity, useRef } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { finishReplayedAnimations, useQuietReveal } from "./quiet-reveal";

// jsdom has neither CSS animations nor getAnimations; stand in for Chromium's.
class FakeCSSAnimation {
	finish = vi.fn();
	constructor(readonly iterations: number) {}
	get effect() {
		return { getTiming: () => ({ iterations: this.iterations }) };
	}
}

let animations: object[];

beforeEach(() => {
	animations = [];
	vi.stubGlobal("CSSAnimation", FakeCSSAnimation);
	Object.defineProperty(Element.prototype, "getAnimations", { configurable: true, value: () => animations });
});

afterEach(() => {
	vi.unstubAllGlobals();
	Reflect.deleteProperty(Element.prototype, "getAnimations");
});

describe("finishReplayedAnimations", () => {
	it("finishes one-shot CSS animations and leaves loops and script animations running", () => {
		const entrance = new FakeCSSAnimation(1);
		const spinner = new FakeCSSAnimation(Infinity);
		const script = { finish: vi.fn() };
		animations = [entrance, spinner, script];

		finishReplayedAnimations(document.createElement("div"));

		expect(entrance.finish).toHaveBeenCalledOnce();
		expect(spinner.finish).not.toHaveBeenCalled();
		expect(script.finish).not.toHaveBeenCalled();
	});
});

function KeptView({ onRow }: { onRow?: (row: HTMLElement | null) => void }) {
	const ref = useRef<HTMLDivElement>(null);
	useQuietReveal(ref);
	return (
		<div ref={ref}>
			<p ref={onRow}>A sent prompt</p>
		</div>
	);
}

describe("useQuietReveal", () => {
	it("finishes replayed CSS entrances in the frame a kept view is shown again, and only then", async () => {
		const entrance = new FakeCSSAnimation(1);
		animations = [entrance];
		const nextFrame = () => act(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
		const view = render(<Activity mode="visible"><KeptView /></Activity>);
		await nextFrame();
		expect(entrance.finish).not.toHaveBeenCalled();

		act(() => view.rerender(<Activity mode="hidden"><KeptView /></Activity>));
		await nextFrame();
		expect(entrance.finish).not.toHaveBeenCalled();

		act(() => view.rerender(<Activity mode="visible"><KeptView /></Activity>));
		expect(entrance.finish).not.toHaveBeenCalled();
		await nextFrame();
		expect(entrance.finish).toHaveBeenCalledOnce();
	});

	it("does not finish anything when the view hides again before that frame", async () => {
		const entrance = new FakeCSSAnimation(1);
		animations = [entrance];
		const view = render(<Activity mode="visible"><KeptView /></Activity>);
		act(() => view.rerender(<Activity mode="hidden"><KeptView /></Activity>));
		act(() => view.rerender(<Activity mode="visible"><KeptView /></Activity>));
		act(() => view.rerender(<Activity mode="hidden"><KeptView /></Activity>));
		await act(() => new Promise<void>((resolve) => requestAnimationFrame(() => resolve())));
		expect(entrance.finish).not.toHaveBeenCalled();
	});

	it("puts back the Motion values a remount on reveal reset to their initial", () => {
		const jump = vi.fn();
		const latestValues: Record<string, number> = { opacity: 1, y: 0 };
		let row: HTMLElement | null = null;
		const visual = {
			latestValues,
			getValue: () => ({ jump }),
			get current() {
				return row;
			},
		} as unknown as VisualElement;
		const onRow = (element: HTMLElement | null) => {
			row = element;
			if (element) visualElementStore.set(element, visual);
		};
		const view = render(<Activity mode="visible"><KeptView onRow={onRow} /></Activity>);

		act(() => view.rerender(<Activity mode="hidden"><KeptView onRow={onRow} /></Activity>));
		// Motion's mount() after a ref detach: back to `initial` (opacity 0), to animate in again.
		latestValues.opacity = 0;
		act(() => view.rerender(<Activity mode="visible"><KeptView onRow={onRow} /></Activity>));

		expect(jump).toHaveBeenCalledOnce();
		expect(jump).toHaveBeenCalledWith(1);
		expect(latestValues).toEqual({ opacity: 1, y: 0 });
	});
});
