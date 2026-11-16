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
	it("selects the filter, counts each segment and reports only a new choice", () => {
		const onChange = vi.fn();
		const root = render(<PRFilterControl filter="open" counts={counts} onChange={onChange} />);
		const picker = root.findByType("Picker" as never);
		expect(picker.props.selection).toBe("open");
		expect(picker.findAllByType("SwiftText" as never).map((segment: ReactTestInstance) => segment.props.children)).toEqual(["Open 4", "Merged 10", "All 16"]);
		act(() => picker.props.onSelectionChange("open"));
		expect(onChange).not.toHaveBeenCalled();
		act(() => picker.props.onSelectionChange("merged"));
		expect(onChange).toHaveBeenCalledWith("merged");
		expect(select).toHaveBeenCalledTimes(1);
	});
});

describe("PRFilterControl on Android", () => {
	it("selects the filter, counts each segment and reports only a new choice", () => {
		const onChange = vi.fn();
		const root = render(<AndroidPRFilterControl filter="merged" counts={counts} onChange={onChange} />);
		const buttons = root.findAllByType("SegmentedButton" as never);
		expect(buttons.map((button: ReactTestInstance) => button.findByType("ComposeText" as never).props.children)).toEqual(["Open 4", "Merged 10", "All 16"]);
		expect(buttons.map((button: ReactTestInstance) => button.props.selected)).toEqual([false, true, false]);
		const [open, merged] = buttons;
		act(() => merged.props.onClick());
		expect(onChange).not.toHaveBeenCalled();
		act(() => open.props.onClick());
		expect(onChange).toHaveBeenCalledWith("open");
		expect(select).toHaveBeenCalledTimes(1);
	});
});
