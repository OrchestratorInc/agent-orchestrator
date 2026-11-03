import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { createElement } from "react";
import { afterEach, expect, it, vi } from "vitest";
import type { WorkerRowInteractionProps } from "./worker-row-interaction.types";

// The pan's callbacks, captured from the gesture builder so the test can drive a drag.
const pan = vi.hoisted(() => ({ handlers: {} as Record<string, (event?: any) => void>, finish: [] as ((result: { finished: boolean }) => void)[] }));

vi.mock("react-native", () => {
	class Value {
		setValue() {}
		stopAnimation() {}
	}
	return {
		Animated: {
			Value,
			View: "AnimatedView",
			timing: () => ({ start: (callback?: (result: { finished: boolean }) => void) => { if (callback) pan.finish.push(callback); } }),
		},
		Modal: "Modal",
		Pressable: "Pressable",
		StyleSheet: { create: (styles: unknown) => styles, hairlineWidth: 1, absoluteFill: {} },
		Text: "Text",
		View: "View",
	};
});
vi.mock("react-native-gesture-handler", () => {
	const builder = (name: string) => {
		const self: Record<string, unknown> = {};
		for (const method of ["runOnJS", "activeOffsetX", "failOffsetY", "minDuration", "maxDistance"]) self[method] = () => self;
		for (const method of ["onStart", "onUpdate", "onEnd"]) {
			self[method] = (callback: (event?: any) => void) => {
				if (name === "pan") pan.handlers[method] = callback;
				return self;
			};
		}
		return self;
	};
	return {
		Gesture: { Pan: () => builder("pan"), LongPress: () => builder("longPress"), Tap: () => builder("tap"), Race: () => ({}) },
		GestureDetector: ({ children }: { children: React.ReactNode }) => createElement("GestureDetector", null, children),
	};
});
vi.mock("./icons", () => ({ Feather: "Feather" }));
vi.mock("./haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ bgElevated: "elevated", borderSubtle: "divider", borderDefault: "border", textPrimary: "primary", textSecondary: "secondary", red: "red", tintRed: "tint-red", accentTint: "accent-tint", bgElevatedHover: "hover" }),
	useThemedStyles: (factory: (theme: Record<string, string>) => unknown) => factory({}),
}));

import { WorkerRowInteraction } from "./worker-row-interaction.android";

let renderer: ReactTestRenderer | undefined;
afterEach(() => {
	act(() => renderer?.unmount());
	pan.handlers = {};
	pan.finish = [];
});
const props: WorkerRowInteractionProps = {
	sessionId: "worker", enabled: true, children: <></>, rightActions: createElement("RailControls"),
	shellStyle: {}, foregroundStyle: {}, rowStyle: {}, pressedStyle: {},
	accessibilityLabel: "Worker", accessibilityHint: "Swipe for actions", onPress() {},
	actions: [], onAction() {}, onSwipeOpen() {}, onSwipeClose() {},
};
function mount() {
	Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
	act(() => { renderer = create(<WorkerRowInteraction {...props} />); });
	return renderer!;
}
const railControls = (view: ReactTestRenderer) => view.root.findAll((node) => node.type === ("RailControls" as never));

it("does not build the action rail's controls behind a resting row", () => {
	expect(railControls(mount())).toHaveLength(0);
});

it("mounts the controls when a drag starts and releases them once the row settles closed", () => {
	const view = mount();
	act(() => pan.handlers.onStart());
	expect(railControls(view)).toHaveLength(1);
	// Swiped open: the controls stay.
	act(() => pan.handlers.onEnd({ translationX: -200, velocityX: -1000 }));
	act(() => pan.finish.shift()!({ finished: true }));
	expect(railControls(view)).toHaveLength(1);
	// Dragged back past the threshold: kept through the closing motion, then dropped.
	act(() => pan.handlers.onStart());
	act(() => pan.handlers.onEnd({ translationX: 200, velocityX: 1000 }));
	expect(railControls(view)).toHaveLength(1);
	act(() => pan.finish.shift()!({ finished: true }));
	expect(railControls(view)).toHaveLength(0);
});

it("keeps the controls when a closing motion is interrupted by a new drag", () => {
	const view = mount();
	act(() => pan.handlers.onStart());
	act(() => pan.handlers.onEnd({ translationX: -10, velocityX: 0 }));
	act(() => pan.handlers.onStart());
	act(() => pan.finish.shift()!({ finished: false }));
	expect(railControls(view)).toHaveLength(1);
});
