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

import { EffortGauge, EffortGaugeButton } from "./effort-gauge";

const levels = (selected?: string) => ["low", "medium", "high", "xhigh", "max"].map((value) => ({ value, label: value[0].toUpperCase() + value.slice(1), selected: value === selected }));

let renderer: ReactTestRenderer | undefined;
beforeEach(() => { fixtures.tap.mockClear(); });
afterEach(() => { act(() => renderer?.unmount()); renderer = undefined; });

const hostType = (node: ReactTestInstance): string => (typeof node.type === "string" ? node.type : "");
const hosts = (root: ReactTestInstance, type: string) => root.findAll((node) => hostType(node) === type);

describe("effort gauge", () => {
	it("draws a segment per level, lit in the theme's accent up to the chosen one", async () => {
		await act(async () => { renderer = create(<EffortGauge choices={levels("medium")} color="#ink" />); });
		const segments = hosts(renderer!.root, "Path");
		expect(segments).toHaveLength(5);
		expect(segments.map((segment) => segment.props.stroke)).toEqual(["#accent", "#accent", "#ink", "#ink", "#ink"]);
	});

	it("names the level on its trigger and ticks on a tap", async () => {
		const onPress = vi.fn();
		await act(async () => { renderer = create(<EffortGaugeButton choices={levels("xhigh")} open={false} onPress={onPress} disabled={false} testID="gauge" />); });
		const button = hosts(renderer!.root, "Pressable")[0];
		expect(button.props.accessibilityLabel).toBe("Effort, Xhigh");
		await act(async () => button.props.onPress());
		expect(onPress).toHaveBeenCalledOnce();
		expect(fixtures.tap).toHaveBeenCalledOnce();
	});
});
