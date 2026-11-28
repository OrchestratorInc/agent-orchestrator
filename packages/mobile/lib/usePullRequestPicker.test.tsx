import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	back: new Set<() => boolean>(),
	keyboard: new Map<string, Set<() => void>>(),
}));

vi.mock("react-native", () => ({
	BackHandler: { addEventListener: (_event: string, handler: () => boolean) => { fixtures.back.add(handler); return { remove: () => fixtures.back.delete(handler) }; } },
	Keyboard: {
		addListener: (event: string, handler: () => void) => {
			const set = fixtures.keyboard.get(event) ?? new Set();
			fixtures.keyboard.set(event, set);
			set.add(handler);
			return { remove: () => set.delete(handler) };
		},
	},
}));

import { markTouchInside, rootTouchStart } from "./touchOutside";
import { usePullRequestPickerOpen } from "./usePullRequestPicker";

let renderer: ReactTestRenderer | undefined;
let picker: ReturnType<typeof usePullRequestPickerOpen>;
beforeEach(() => { vi.useFakeTimers(); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); fixtures.back.clear(); fixtures.keyboard.clear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.useRealTimers(); vi.unstubAllGlobals(); });

function Harness({ count, busy }: { count: number; busy: boolean }) {
	picker = usePullRequestPickerOpen(count, busy);
	return null;
}

function render(count = 3, busy = false) {
	act(() => { renderer = create(<Harness count={count} busy={busy} />); });
	return { update: (next: { count?: number; busy?: boolean }) => act(() => renderer!.update(<Harness count={next.count ?? count} busy={next.busy ?? busy} />)) };
}

const emitKeyboard = (event: string) => act(() => { for (const listener of [...(fixtures.keyboard.get(event) ?? [])]) listener(); });

describe("pull request list open", () => {
	it("opens and closes on a touch outside, not on its own", () => {
		render();
		act(() => picker.show());
		expect(picker.open).toBe(true);
		act(() => { markTouchInside(); rootTouchStart(); });
		expect(picker.open).toBe(true);
		act(() => rootTouchStart());
		expect(picker.open).toBe(false);
	});

	it("has no timeout: it is a list to read", () => {
		render();
		act(() => picker.show());
		act(() => { vi.advanceTimersByTime(60_000); });
		expect(picker.open).toBe(true);
	});

	it("closes when the keyboard shows and on back", () => {
		render();
		act(() => picker.show());
		emitKeyboard("keyboardDidShow");
		expect(picker.open).toBe(false);
		act(() => picker.show());
		let handled = false;
		act(() => { for (const handler of [...fixtures.back]) handled = handler(); });
		expect(handled).toBe(true);
		expect(picker.open).toBe(false);
	});

	// Review Focus 4: the keyboard closing (a chip tap with it up) leaves the list open.
	it("stays open as the keyboard hides", () => {
		render();
		act(() => picker.show());
		emitKeyboard("keyboardDidHide");
		emitKeyboard("keyboardWillHide");
		expect(picker.open).toBe(true);
	});

	// Review Focus 2: the session's PRs change while it is up.
	it("closes when fewer than two remain, or while busy", () => {
		const { update } = render(3);
		act(() => picker.show());
		update({ count: 1 });
		expect(picker.open).toBe(false);
		update({ count: 3 });
		act(() => picker.show());
		update({ busy: true });
		expect(picker.open).toBe(false);
	});

	it("never opens for one pull request", () => {
		render(1);
		act(() => picker.show());
		expect(picker.open).toBe(false);
		act(() => picker.toggle());
		expect(picker.open).toBe(false);
	});

	it("toggles", () => {
		render();
		act(() => picker.toggle());
		expect(picker.open).toBe(true);
		act(() => picker.toggle());
		expect(picker.open).toBe(false);
	});
});
