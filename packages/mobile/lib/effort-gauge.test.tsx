import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: new Proxy({}, { get: (_target, key) => `#${String(key)}` }),
	tap: vi.fn(),
}));

vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemedStyles: (make: (theme: object) => object) => make(fixtures.theme) }));
vi.mock("./haptics", () => ({ haptics: { tap: fixtures.tap } }));
vi.mock("react-native", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Pressable: host("Pressable"), StyleSheet: { create: (styles: object) => styles }, View: host("View") };
});
vi.mock("react-native-svg", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { default: host("Svg"), Circle: host("Circle"), Line: host("Line"), Path: host("Path") };
});

import { EffortGaugeButton } from "./effort-gauge";

const levels = (selected?: string) => ["low", "medium", "high", "xhigh", "max"].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1), selected: value === selected }));

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { fixtures.tap.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const hosts = (root: ReactTestInstance, type: string) => root.findAll((node) => hostType(node) === type);

describe("effort gauge", () => {
	it("names the level on its trigger and ticks on a tap", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<EffortGaugeButton choices={levels("xhigh")} open={false} onPress={onPress} disabled={false} testID="gauge" />); });
		const button = hosts(renderer!.root, "Pressable")[0];
		expect(button.props.accessibilityLabel).toBe("Effort, Xhigh");
		await act(async () => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
		expect(fixtures.tap).toHaveBeenCalledOnce();
	});

	// Waiting for the finger to lift read as lag on the slider opening.
	it("toggles as a finger lands, once per touch, and still on a screen reader's activation", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<EffortGaugeButton choices={levels("high")} open={false} onPress={onPress} disabled={false} testID="gauge" />); });
		const button = () => hosts(renderer!.root, "Pressable")[0];
		await act(async () => button().props.onPressIn());
		expect(onPress).toHaveBeenCalledOnce();
		// The same touch's press, as the finger lifts, does nothing more.
		await act(async () => button().props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
		// An activation with no touch before it (TalkBack, VoiceOver) toggles.
		await act(async () => button().props.onPress());
		expect(onPress).toHaveBeenCalledTimes(2);
	});

	// React Native fires onPressIn again when a held finger slides off and back on.
	it("toggles once when the same finger slides off and back on", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<EffortGaugeButton choices={levels("high")} open={false} onPress={onPress} disabled={false} testID="gauge" />); });
		const button = () => hosts(renderer!.root, "Pressable")[0];
		await act(async () => button().props.onPressIn());
		await act(async () => button().props.onPressIn());
		expect(onPress).toHaveBeenCalledOnce();
	});

	// A finger let go outside the gauge gets no onPress to clear the touch.
	it("still toggles on a screen reader's activation after a touch released outside", async () => {
		vi.useFakeTimers();
		try {
			const onPress = vi.fn();
			await act(async () => { renderer = create(<EffortGaugeButton choices={levels("high")} open={false} onPress={onPress} disabled={false} testID="gauge" />); });
			const button = () => hosts(renderer!.root, "Pressable")[0];
			await act(async () => button().props.onPressIn());
			await act(async () => { button().props.onTouchEnd(); vi.runAllTimers(); });
			await act(async () => button().props.onPress());
			expect(onPress).toHaveBeenCalledTimes(2);
			// And the next touch toggles again.
			await act(async () => button().props.onPressIn());
			expect(onPress).toHaveBeenCalledTimes(3);
		} finally {
			vi.useRealTimers();
		}
	});
});
