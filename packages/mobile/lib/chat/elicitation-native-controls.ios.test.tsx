import { useState } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
const { native } = vi.hoisted(() => {
	const native = { value: "", get: () => native.value, set: vi.fn((value: string) => { native.value = value; }) };
	return { native };
});
vi.mock("@expo/ui", () => ({ Host: "Host" }));
vi.mock("@expo/ui/swift-ui", () => ({
	Button: "Button", Divider: "Divider", HStack: "HStack", Image: "Image", Spacer: "Spacer",
	Text: "Text", TextField: "TextField", VStack: "VStack", useNativeState: () => native,
}));
// Each modifier records its name and arguments, so a test can read what a view was given.
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	...Object.fromEntries([
		"accessibilityAddTraits", "accessibilityHidden", "buttonStyle", "contentShape", "controlSize", "disabled", "fixedSize", "font", "foregroundStyle", "frame",
		"lineLimit", "minimumScaleFactor", "multilineTextAlignment", "padding", "textFieldStyle", "tint",
	].map((name) => [name, (...args: unknown[]) => ({ [name]: args })])),
	shapes: { rectangle: () => ({ shape: "rectangle" }) },
}));
vi.mock("../glass", () => ({ glassPanel: () => ({}) }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemeState: () => ({ scheme: "light" }) }));

import { Host } from "@expo/ui";
import { Button, HStack, Spacer, Text, TextField } from "@expo/ui/swift-ui";
import { haptics } from "../haptics";
import { ElicitationAction, ElicitationActionRow, ElicitationTextField } from "./elicitation-native-controls.ios";

let renderer: ReactTestRenderer | undefined;
let resetValue: (value: string | number) => void;
function Harness({ numeric = false }: { numeric?: boolean }) {
	const [value, setValue] = useState<string | number>("");
	resetValue = setValue;
	return <ElicitationTextField value={value} label="Conversation title" numeric={numeric} onChange={setValue} />;
}
beforeEach(() => { native.value = ""; native.set.mockClear(); });
afterEach(() => { if (renderer) act(() => renderer!.unmount()); renderer = undefined; });

it("does not write a delayed React acknowledgement over newer native input, but accepts external resets", () => {
	act(() => { renderer = create(<Harness />); });
	native.value = "Mobile QA verified";
	act(() => renderer!.root.findByType(TextField).props.onTextChange("Mob"));
	expect(native.value).toBe("Mobile QA verified");
	expect(native.set).not.toHaveBeenCalled();
	act(() => renderer!.root.findByType(TextField).props.onTextChange("Mobile QA verified"));
	expect(native.set).not.toHaveBeenCalled();
	act(() => resetValue(""));
	expect(native.value).toBe("");
	act(() => resetValue("Mobile QA verified"));
	expect(native.value).toBe("Mobile QA verified");
});

it("preserves numeric input while acknowledging its normalized value and applies external changes", () => {
	act(() => { renderer = create(<Harness numeric />); });
	native.value = "1.";
	act(() => renderer!.root.findByType(TextField).props.onTextChange("1."));
	expect(native.value).toBe("1.");
	expect(native.set).not.toHaveBeenCalled();
	act(() => resetValue(2));
	expect(native.value).toBe("2");
});

it("forwards the newest buffered callback after the parent acknowledges the older value", () => {
	const onChange = vi.fn();
	const field = (value: string | number, numeric = false) => <ElicitationTextField value={value} label="Title" numeric={numeric} onChange={onChange} />;
	act(() => { renderer = create(field("")); });
	native.value = "A"; act(() => renderer!.root.findByType(TextField).props.onTextChange("A"));
	native.value = "AB"; act(() => renderer!.root.findByType(TextField).props.onTextChange("AB"));
	expect(onChange.mock.calls.map(call => call[0])).toEqual(["A"]);
	act(() => renderer!.update(field("A")));
	expect(onChange.mock.calls.map(call => call[0])).toEqual(["A", "AB"]);
	expect(native.value).toBe("AB"); expect(native.set).not.toHaveBeenCalled();
	act(() => renderer!.update(field("AB")));
	expect(native.set).not.toHaveBeenCalled();
	act(() => renderer!.update(field("")));
	expect(native.value).toBe("");
	act(() => renderer!.update(field("A")));
	expect(native.value).toBe("A");
});

it("preserves newer numeric lexical input across multiple normalized acknowledgements", () => {
	const field = (value: string | number) => <ElicitationTextField value={value} label="Amount" numeric onChange={vi.fn()} />;
	act(() => { renderer = create(field("")); });
	native.value = "1."; act(() => renderer!.root.findByType(TextField).props.onTextChange("1."));
	native.value = "12."; act(() => renderer!.root.findByType(TextField).props.onTextChange("12."));
	act(() => renderer!.update(field(1)));
	expect(native.value).toBe("12."); expect(native.set).not.toHaveBeenCalled();
	act(() => renderer!.update(field(12)));
	expect(native.set).not.toHaveBeenCalled();
	act(() => renderer!.update(field(3)));
	expect(native.value).toBe("3");
});


it.each([false, true])("handles repeated native values followed by an external reset (numeric: %s)", (numeric) => {
	act(() => { renderer = create(<Harness numeric={numeric} />); });
	const values = numeric ? ["1.", "12.", "1."] : ["A", "AB", "A"];
	act(() => {
		for (const value of values) {
			native.value = value;
			renderer!.root.findByType(TextField).props.onTextChange(value);
		}
	});
	expect(native.value).toBe(values[2]);
	expect(native.set).not.toHaveBeenCalled();
	act(() => resetValue(numeric ? 12 : "AB"));
	expect(native.value).toBe(numeric ? "12" : "AB");
});

// The measuring copy renders first, then the button people see.
function actionParts() {
	const [measure, visible] = renderer!.root.findAllByType(Host);
	const [probe, button] = renderer!.root.findAllByType(Button);
	const [, label] = renderer!.root.findAllByType(Text);
	return { measure, visible, probe, button, label };
}
function modifier(node: ReactTestInstance, name: string): unknown {
	return (node.props.modifiers as Record<string, unknown>[]).find((entry) => name in entry)?.[name];
}
function measure(row: number, label: number) {
	act(() => {
		actionParts().measure.props.onLayout({ nativeEvent: { layout: { width: row } } });
		actionParts().measure.props.onLayoutContent({ nativeEvent: { width: label, height: 50 } });
	});
}

it("lets an action take its label's width instead of a guessed one", () => {
	const onPress = vi.fn();
	act(() => { renderer = create(<ElicitationAction label="Allow once" primary onPress={onPress} />); });
	const { measure: copy, probe, button, label } = actionParts();
	expect(label.props.children).toBe("Allow once");
	expect(modifier(button, "frame")).toBeUndefined();
	// The copy is out of reach of touch and screen readers.
	expect(copy.props.accessibilityElementsHidden).toBe(true);
	expect(modifier(probe, "accessibilityHidden")).toEqual([true]);
	expect(probe.props.onPress).toBeUndefined();
	measure(320, 140);
	expect(modifier(actionParts().button, "frame")).toBeUndefined();
	act(() => actionParts().button.props.onPress());
	expect(onPress).toHaveBeenCalledOnce();
});

it("wraps a decision wider than its row instead of running past the card", () => {
	const decision = "No, and tell me what to do differently (esc)";
	const action = (label: string) => <ElicitationAction label={label} primary onPress={vi.fn()} />;
	act(() => { renderer = create(action(decision)); });
	measure(300.6, 420);
	expect(modifier(actionParts().button, "frame")).toEqual([{ width: 300 }]);
	// The copy is never held, so it keeps reporting the label's own width.
	expect(modifier(actionParts().probe, "frame")).toBeUndefined();
	// Given room, it goes back to its own size.
	measure(480, 420);
	expect(modifier(actionParts().button, "frame")).toBeUndefined();
	// A new label is measured afresh; the old one's width says nothing about it.
	measure(300, 420);
	act(() => renderer!.update(action("Sending…")));
	expect(modifier(actionParts().button, "frame")).toBeUndefined();
	measure(300, 96);
	expect(modifier(actionParts().button, "frame")).toBeUndefined();
});

// Plain secondary buttons had no shape and almost no gap, so "Allow Once" and
// "Always Allow" read as one line of text beside the prominent button.
it("draws secondary actions as bordered buttons the primary's size, at least a touch target", () => {
	act(() => { renderer = create(<ElicitationAction label="Skip" onPress={vi.fn()} />); });
	expect(modifier(actionParts().button, "buttonStyle")).toEqual(["bordered"]);
	expect(modifier(actionParts().button, "controlSize")).toEqual(["large"]);
	// The button carries the target now, so the label needs no frame of its own.
	expect(actionParts().label.props.modifiers).toBeUndefined();
	act(() => renderer!.update(<ElicitationAction label="Submit" primary onPress={vi.fn()} />));
	expect(modifier(actionParts().button, "buttonStyle")).toEqual(["borderedProminent"]);
	expect(modifier(actionParts().button, "controlSize")).toEqual(["large"]);
});

it("taps on press unless the caller buzzes for the outcome", () => {
	const onPress = vi.fn();
	vi.mocked(haptics.tap).mockClear();
	act(() => { renderer = create(<ElicitationAction label="Cancel" onPress={onPress} />); });
	act(() => actionParts().button.props.onPress());
	expect(haptics.tap).toHaveBeenCalledOnce();
	act(() => renderer!.update(<ElicitationAction label="Save" primary reportsOutcome onPress={onPress} />));
	act(() => actionParts().button.props.onPress());
	expect(haptics.tap).toHaveBeenCalledOnce();
	expect(onPress).toHaveBeenCalledTimes(2);
});

// The timeline reveals a request field when it takes focus; losing it asks nothing.
it("reports the field taking focus", () => {
	const onFocus = vi.fn();
	act(() => { renderer = create(<ElicitationTextField value="" label="Title" onChange={vi.fn()} onFocus={onFocus} />); });
	act(() => renderer!.root.findByType(TextField).props.onFocusChange(true));
	act(() => renderer!.root.findByType(TextField).props.onFocusChange(false));
	expect(onFocus).toHaveBeenCalledOnce();
});

// An approval's decisions share one line, whatever their labels or the text
// size: one native row lays them out, and a crowded row scales labels down a
// little (then truncates) rather than wrapping the last decision under the rest.
describe("action row", () => {
	const decisions = () => [
		{ key: "reject", label: "Deny", onPress: vi.fn() },
		{ key: "allow", label: "Allow Once", primary: true, onPress: vi.fn() },
		{ key: "always", label: "Always Allow", onPress: vi.fn() },
	];

	it("lays every decision out in one native row, right-aligned", () => {
		act(() => { renderer = create(<ElicitationActionRow actions={decisions()} reportsOutcome />); });
		expect(renderer!.root.findAllByType(Host)).toHaveLength(1);
		const row = renderer!.root.findByType(HStack);
		const [spacer, ...buttons] = row.children as ReactTestInstance[];
		expect(spacer.type).toBe(Spacer);
		expect(spacer.props.minLength).toBe(0);
		expect(buttons.map((button) => button.findByType(Text).props.children)).toEqual(["Deny", "Allow Once", "Always Allow"]);
	});

	// The answer choices' type: at the default size three large buttons then fit
	// at one size, where body text had the longer labels scaled down unevenly.
	it("keeps each label on one line at one size, scaling it before it would wrap", () => {
		act(() => { renderer = create(<ElicitationActionRow actions={decisions()} reportsOutcome />); });
		for (const label of renderer!.root.findAllByType(Text)) {
			expect(modifier(label, "font")).toEqual([{ textStyle: "subheadline", weight: "semibold" }]);
			expect(modifier(label, "lineLimit")).toEqual([1]);
			expect(modifier(label, "minimumScaleFactor")).toEqual([0.7]);
		}
	});

	it("draws the primary prominent and the rest bordered, all at least the 44pt target", () => {
		act(() => { renderer = create(<ElicitationActionRow actions={decisions()} reportsOutcome />); });
		const buttons = renderer!.root.findAllByType(Button);
		expect(buttons.map((button) => modifier(button, "buttonStyle"))).toEqual([["bordered"], ["borderedProminent"], ["bordered"]]);
		for (const button of buttons) expect(modifier(button, "controlSize")).toEqual(["large"]);
	});

	it("presses the decision tapped, buzzing only when the caller doesn't", () => {
		const actions = decisions();
		vi.mocked(haptics.tap).mockClear();
		act(() => { renderer = create(<ElicitationActionRow actions={actions} reportsOutcome />); });
		act(() => renderer!.root.findAllByType(Button)[2].props.onPress());
		expect(actions[2].onPress).toHaveBeenCalledOnce();
		expect(actions[0].onPress).not.toHaveBeenCalled();
		expect(haptics.tap).not.toHaveBeenCalled();
		act(() => renderer!.update(<ElicitationActionRow actions={actions} />));
		act(() => renderer!.root.findAllByType(Button)[0].props.onPress());
		expect(haptics.tap).toHaveBeenCalledOnce();
	});
});
