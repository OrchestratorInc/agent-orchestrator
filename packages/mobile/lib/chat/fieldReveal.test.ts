import { afterEach, describe, expect, it, vi } from "vitest";
import { createFieldReveals, fieldRevealOffset } from "./fieldReveal";

// The region the list shows: below the header at 100, above the composer on the keyboard at 500.
const region = { visibleTop: 100, visibleBottom: 500, offset: 1_000 };

describe("revealing a focused request field", () => {
	// Focusing a field already in view must not nudge the conversation (the
	// jitter `scrollsChildToFocus={false}` removed).
	it("leaves a field in view where it is", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 200, fieldBottom: 260 })).toBeUndefined();
		expect(fieldRevealOffset({ ...region, fieldTop: 100, fieldBottom: 500 })).toBeUndefined();
	});

	it("lifts a field under the keyboard just clear of it", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 560, fieldBottom: 620 })).toBe(1_120);
		// Partly hidden: only the hidden part.
		expect(fieldRevealOffset({ ...region, fieldTop: 470, fieldBottom: 530 })).toBe(1_030);
	});

	it("brings down a field above the region, under the header", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 40, fieldBottom: 100 })).toBe(940);
	});

	// A field that fits shows whole: its label and hint come with its input.
	it("shows the label and hint with the input when the whole field fits", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 450, fieldBottom: 650, inputTop: 610, inputBottom: 650 })).toBe(1_150);
		expect(fieldRevealOffset({ ...region, fieldTop: 40, fieldBottom: 240, inputTop: 200, inputBottom: 240 })).toBe(940);
	});

	// A long hint can make the field taller than the space above the keyboard;
	// showing that field from its top left its input under the keyboard.
	it("shows the input of a field taller than the region", () => {
		// Field 100-700 in a 100-500 region: its input comes up just clear of the keyboard.
		expect(fieldRevealOffset({ ...region, fieldTop: 100, fieldBottom: 700, inputTop: 660, inputBottom: 700 })).toBe(1_200);
		expect(fieldRevealOffset({ ...region, fieldTop: 300, fieldBottom: 900, inputTop: 860, inputBottom: 900 })).toBe(1_400);
		// Above the region: down until the input clears the header.
		expect(fieldRevealOffset({ ...region, fieldTop: -600, fieldBottom: 80, inputTop: 40, inputBottom: 80 })).toBe(940);
		// Its input already in view: nothing moves, though the label is out of sight.
		expect(fieldRevealOffset({ ...region, fieldTop: -100, fieldBottom: 480, inputTop: 440, inputBottom: 480 })).toBeUndefined();
	});

	// With no input measured the field is its own input: a tall one shows from its top.
	it("shows a field taller than the region from its top when its input is not known", () => {
		expect(fieldRevealOffset({ ...region, fieldTop: 300, fieldBottom: 900 })).toBe(1_200);
		expect(fieldRevealOffset({ ...region, fieldTop: 50, fieldBottom: 650 })).toBe(950);
	});
});

describe("field reveals while the keyboard opens", () => {
	afterEach(() => vi.useRealTimers());

	function keyboard(visible = false) {
		const listeners = new Set<() => void>();
		return {
			listeners,
			isVisible: () => visible,
			addListener: (_event: "keyboardDidShow", listener: () => void) => {
				listeners.add(listener);
				return { remove: () => { listeners.delete(listener); } };
			},
			show() {
				visible = true;
				for (const listener of [...listeners]) listener();
			},
		};
	}

	it("measure once the keyboard has opened, or once the wait runs out without one", () => {
		vi.useFakeTimers();
		const opening = keyboard();
		const measured: string[] = [];
		createFieldReveals(opening, 600).request(() => measured.push("a"));
		expect(measured).toEqual([]);
		opening.show();
		expect(measured).toEqual(["a"]);
		vi.advanceTimersByTime(600);
		expect(measured).toEqual(["a"]);
		expect(opening.listeners.size).toBe(0);

		// A hardware keyboard sends no keyboardDidShow.
		const hardware = keyboard();
		createFieldReveals(hardware, 600).request(() => measured.push("b"));
		vi.advanceTimersByTime(599);
		expect(measured).toEqual(["a"]);
		vi.advanceTimersByTime(1);
		expect(measured).toEqual(["a", "b"]);
		expect(hardware.listeners.size).toBe(0);
	});

	it("measure at once with the keyboard already up", () => {
		vi.useFakeTimers();
		const measure = vi.fn();
		createFieldReveals(keyboard(true), 600).request(measure);
		vi.advanceTimersByTime(0);
		expect(measure).toHaveBeenCalledTimes(1);
	});

	// Focus A, then B before A's wait runs out: A's timer scrolled back to A.
	it("cancel an earlier field's wait when a later one takes focus", () => {
		vi.useFakeTimers();
		const opening = keyboard();
		const reveals = createFieldReveals(opening, 600);
		const measured: string[] = [];
		reveals.request(() => measured.push("a"));
		vi.advanceTimersByTime(300);
		reveals.request(() => measured.push("b"));
		expect(opening.listeners.size).toBe(1);
		vi.advanceTimersByTime(600);
		expect(measured).toEqual(["b"]);
		opening.show();
		expect(measured).toEqual(["b"]);
	});

	it("turn an earlier field's measurement stale when a later one takes focus", () => {
		vi.useFakeTimers();
		const opening = keyboard();
		const reveals = createFieldReveals(opening, 600);
		let first!: () => boolean;
		reveals.request((isCurrent) => { first = isCurrent; });
		opening.show();
		expect(first()).toBe(true);
		let second!: () => boolean;
		reveals.request((isCurrent) => { second = isCurrent; });
		vi.advanceTimersByTime(0);
		expect(first()).toBe(false);
		expect(second()).toBe(true);
	});

	it("drop the pending reveal when cancelled, as when the timeline unmounts", () => {
		vi.useFakeTimers();
		const opening = keyboard();
		const reveals = createFieldReveals(opening, 600);
		const measure = vi.fn();
		reveals.request(measure);
		reveals.cancel();
		expect(opening.listeners.size).toBe(0);
		opening.show();
		vi.advanceTimersByTime(600);
		expect(measure).not.toHaveBeenCalled();
	});
});
