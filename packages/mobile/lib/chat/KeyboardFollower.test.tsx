import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

const keyboard = vi.hoisted(() => ({ height: { value: 0 } }));
vi.mock("react-native-keyboard-controller", () => ({ useReanimatedKeyboardAnimation: () => keyboard }));
vi.mock("react-native-reanimated", async () => {
	const React = await import("react");
	return {
		default: { View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("AnimatedView", props, children) },
		useAnimatedStyle: (style: () => unknown) => style,
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
