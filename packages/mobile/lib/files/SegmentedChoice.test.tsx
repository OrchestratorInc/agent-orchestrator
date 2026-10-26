import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../ThemeProvider", () => ({
	useTheme: () => ({ accentTint: "#accentTint", textPrimary: "#textPrimary", textSecondary: "#textSecondary", borderDefault: "#borderDefault" }),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	return { Host: ({ children, ...props }: { children?: React.ReactNode }) => React.createElement("SwiftHost", props, children) };
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return { Picker: host("Picker"), Text: host("SwiftText") };
});
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	pickerStyle: (style: string) => ({ pickerStyle: style }),
	tag: (value: number) => ({ tag: value }),
	accessibilityIdentifier: (id: string) => ({ accessibilityIdentifier: id }),
}));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("ComposeHost"),
		SingleChoiceSegmentedButtonRow: host("SingleChoiceSegmentedButtonRow"),
		SegmentedButton: Object.assign(host("SegmentedButton"), { Label: host("SegmentedButtonLabel") }),
		Text: host("ComposeText"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	fillMaxWidth: () => ({ fillMaxWidth: true }),
	testID: (tag: string) => ({ testID: tag }),
}));

import { SegmentedChoice } from "./SegmentedChoice";
import { SegmentedChoice as AndroidSegmentedChoice } from "./SegmentedChoice.android";

const options = [
	{ key: "diff", label: "Changes 12" },
	{ key: "preview", label: "Preview" },
	{ key: "source", label: "Source" },
] as const;

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(async () => {
	if (renderer) await act(async () => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("SegmentedChoice on iOS", () => {
	it("is a segmented SwiftUI picker with the count in the label", async () => {
		const onChange = vi.fn();
		await act(async () => { renderer = create(<SegmentedChoice value="preview" options={options} onChange={onChange} accessibilityLabel="View" />); });
		const picker = renderer!.root.findByType("Picker" as never);
		expect(picker.props.modifiers).toContainEqual({ pickerStyle: "segmented" });
		expect(picker.props.selection).toBe(1);
		expect(picker.props.label).toBe("View");
		expect(renderer!.root.findAllByType("SwiftText" as never).map((text) => text.props.children)).toEqual(["Changes 12", "Preview", "Source"]);
		act(() => picker.props.onSelectionChange(2));
		expect(onChange).toHaveBeenCalledWith("source");
		act(() => picker.props.onSelectionChange(1));
		expect(onChange).toHaveBeenCalledTimes(1);
	});
});

describe("SegmentedChoice on Android", () => {
	it("is Material's single-choice segmented row, the chosen segment selected", async () => {
		const onChange = vi.fn();
		await act(async () => { renderer = create(<AndroidSegmentedChoice value="diff" options={options} onChange={onChange} accessibilityLabel="View" testID="file-view" />); });
		expect(renderer!.root.findByType("SingleChoiceSegmentedButtonRow" as never).props.modifiers).toContainEqual({ testID: "file-view" });
		const segments = renderer!.root.findAllByType("SegmentedButton" as never);
		expect(segments.map((segment) => segment.props.selected)).toEqual([true, false, false]);
		expect(segments[0].props.colors.activeContainerColor).toBe("#accentTint");
		act(() => segments[1].props.onClick());
		expect(onChange).toHaveBeenCalledWith("preview");
		act(() => segments[0].props.onClick());
		expect(onChange).toHaveBeenCalledTimes(1);
	});
});
