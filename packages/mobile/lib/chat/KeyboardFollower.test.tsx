import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const keyboard = vi.hoisted(() => ({ height: { value: 0 }, onStart: undefined as undefined | ((event: { height: number }) => void) }));
vi.mock("react-native-keyboard-controller", () => ({
	useReanimatedKeyboardAnimation: () => keyboard,
	useKeyboardHandler: (handler: { onStart(event: { height: number }): void }) => { keyboard.onStart = handler.onStart; },
	useReanimatedFocusedInput: () => ({ input: { value: null } }),
}));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedView", props, children) },
		useAnimatedStyle: (style: () => unknown) => style,
		useSharedValue: <T,>(value: T) => React.useRef({ value }).current,
		runOnJS: <F,>(fn: F) => fn,
	};
});

import { KeyboardFollower } from "./KeyboardFollower";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); keyboard.height.value = 0; });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; vi.unstubAllGlobals(); });

function translateY(lift: number | undefined) {
	act(() => { renderer = create(<KeyboardFollower lift={lift}>{null}</KeyboardFollower>); });
	const view = renderer!.root.findByType("AnimatedView" as never);
	const follow = (view.props.style as unknown[])[1] as () => { transform: { translateY: number }[] };
	return follow().transform[0].translateY;
}

// Padding is layout, and a busy JS thread held it back: the keyboard came up
// over a composer still at the bottom of a long conversation.
it("follows the keyboard by a transform, up by its height", () => {
	keyboard.height.value = -336;
	expect(translateY(undefined)).toBe(-336);
});

it("holds the given lift while the keyboard's progress cannot be trusted", () => {
	keyboard.height.value = 0;
	expect(translateY(312)).toBe(-312);
	expect(translateY(0)).toBe(-0);
});

// Focusing the field re-renders the screen, so it heard of the opening several
// frames late, and the composer stayed down behind the rising keys until then.
it("follows an opening from its first frame, before the screen has heard of it", () => {
	keyboard.height.value = 0;
	act(() => { renderer = create(<KeyboardFollower lift={0}>{null}</KeyboardFollower>); });
	const follow = () => ((renderer!.root.findByType("AnimatedView" as never).props.style as unknown[])[1] as () => { transform: { translateY: number }[] })().transform[0].translateY;
	expect(follow()).toBe(-0);
	keyboard.onStart?.({ height: 336 });
	keyboard.height.value = -120;
	expect(follow()).toBe(-120);
	// A close the screen has settled holds it down again, whatever the keyboard last said.
	keyboard.onStart?.({ height: 0 });
	expect(follow()).toBe(-0);
});
