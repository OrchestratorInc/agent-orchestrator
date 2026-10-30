import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const select = vi.hoisted(() => vi.fn());

vi.mock("react-native", async () => {
	const React = await import("react");
	return {
		StyleSheet: { create: (styles: unknown) => styles },
		View: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("View", props, children),
	};
});
vi.mock("./haptics", () => ({ haptics: { select } }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => new Proxy({}, { get: (_, key) => `#${String(key)}` }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Host: host("SwiftHost"), Picker: host("Picker"), Text: host("SwiftText") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	pickerStyle: (style: string) => ({ pickerStyle: style }),
	tag: (value: string) => ({ tag: value }),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		SingleChoiceSegmentedButtonRow: host("SegmentedRow"),
		SegmentedButton: Object.assign(host("SegmentedButton"), { Label: host("LabelSlot") }),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	fillMaxWidth: () => ({ fillMaxWidth: true }),
	testID: (tag: string) => ({ testID: tag }),
}));

import { PRFilterControl } from "./pr-filter-control";
import { PRFilterControl as AndroidPRFilterControl } from "./pr-filter-control.android";

const counts = { open: 4, merged: 10, all: 16 };
let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	select.mockClear();
});
afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.unstubAllGlobals();
});

function render(element: React.ReactElement) {
	act(() => {
		renderer = create(element);
	});
	return renderer!.root;
}

describe("PRFilterControl on iOS", () => {
	// The system segmented control, with each filter's count in its segment.
	it("is a segmented SwiftUI picker with a count in each segment", () => {
		const root = render(<PRFilterControl filter="open" counts={counts} onChange={vi.fn()} />);
		const picker = root.findByType("Picker" as never);
		expect(picker.props.modifiers).toContainEqual({ pickerStyle: "segmented" });
		expect(picker.props.selection).toBe("open");
		const segments = picker.findAllByType("SwiftText" as never);
		expect(segments.map((segment: ReactTestInstance) => segment.props.children)).toEqual(["Open 4", "Merged 10", "All 16"]);
		expect(segments.map((segment: ReactTestInstance) => segment.props.modifiers)).toEqual([[{ tag: "open" }], [{ tag: "merged" }], [{ tag: "all" }]]);
	});

	it("reports a new choice, and only a new one", () => {
		const onChange = vi.fn();
		const root = render(<PRFilterControl filter="open" counts={counts} onChange={onChange} />);
		const picker = root.findByType("Picker" as never);
		act(() => picker.props.onSelectionChange("open"));
		expect(onChange).not.toHaveBeenCalled();
		act(() => picker.props.onSelectionChange("merged"));
		expect(onChange).toHaveBeenCalledWith("merged");
		expect(select).toHaveBeenCalledTimes(1);
	});
});

describe("PRFilterControl on Android", () => {
	// Compose checks the chosen segment as well as filling it.
	it("is a row of Material 3 segmented buttons with a count in each", () => {
		const root = render(<AndroidPRFilterControl filter="merged" counts={counts} onChange={vi.fn()} />);
		expect(root.findByType("SegmentedRow" as never).props.modifiers).toContainEqual({ fillMaxWidth: true });
		const buttons = root.findAllByType("SegmentedButton" as never);
		expect(buttons.map((button: ReactTestInstance) => button.findByType("ComposeText" as never).props.children)).toEqual(["Open 4", "Merged 10", "All 16"]);
		expect(buttons.map((button: ReactTestInstance) => button.props.selected)).toEqual([false, true, false]);
		expect(buttons[1].props.colors).toMatchObject({ activeContainerColor: "#accentTint", activeContentColor: "#accent" });
	});

	it("reports a new choice, and only a new one", () => {
		const onChange = vi.fn();
		const root = render(<AndroidPRFilterControl filter="merged" counts={counts} onChange={onChange} />);
		const [open, merged] = root.findAllByType("SegmentedButton" as never);
		act(() => merged.props.onClick());
		expect(onChange).not.toHaveBeenCalled();
		act(() => open.props.onClick());
		expect(onChange).toHaveBeenCalledWith("open");
		expect(select).toHaveBeenCalledTimes(1);
	});
});
