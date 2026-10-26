import { useState } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, expect, it, vi } from "vitest";

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
		"multilineTextAlignment", "padding", "textFieldStyle", "tint",
	].map((name) => [name, (...args: unknown[]) => ({ [name]: args })])),
	shapes: { rectangle: () => ({ shape: "rectangle" }) },
}));
vi.mock("../glass", () => ({ glassPanel: () => ({}) }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemeState: () => ({ scheme: "light" }) }));

import { Host } from "@expo/ui";
import { Button, Text, TextField } from "@expo/ui/swift-ui";
import { touchTarget } from "../tokens";
import { ElicitationAction, ElicitationTextField } from "./elicitation-native-controls.ios";

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
	act(() => { renderer = create(<ElicitationAction label="Allow once" primary width={82} onPress={onPress} />); });
	const { measure: copy, visible, probe, button, label } = actionParts();
	expect(visible.props.matchContents).toBe(true);
	expect(visible.props.style).toEqual({ maxWidth: "100%" });
	expect(label.props.children).toBe("Allow once");
	expect(modifier(button, "frame")).toEqual([{ minWidth: 82 }]);
	// The copy spans the row at its natural width, out of reach of touch and screen readers.
	expect(copy.props.matchContents).toBeUndefined();
	expect(copy.props.pointerEvents).toBe("none");
	expect(copy.props.accessibilityElementsHidden).toBe(true);
	expect(copy.props.importantForAccessibility).toBe("no-hide-descendants");
	expect(modifier(probe, "fixedSize")).toEqual([]);
	expect(modifier(probe, "accessibilityHidden")).toEqual([true]);
	expect(probe.props.onPress).toBeUndefined();
	measure(320, 140);
	expect(modifier(actionParts().button, "frame")).toEqual([{ minWidth: 82 }]);
	act(() => actionParts().button.props.onPress());
	expect(onPress).toHaveBeenCalledOnce();
});

it("wraps a decision wider than its row instead of running past the card", () => {
	const decision = "No, and tell me what to do differently (esc)";
	const action = (label: string) => <ElicitationAction label={label} primary onPress={vi.fn()} />;
	act(() => { renderer = create(action(decision)); });
	measure(300.6, 420);
	expect(modifier(actionParts().button, "frame")).toEqual([{ width: 300 }]);
	expect(modifier(actionParts().button, "multilineTextAlignment")).toEqual(["center"]);
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

it("gives every action at least a touch target", () => {
	act(() => { renderer = create(<ElicitationAction label="Skip" onPress={vi.fn()} />); });
	const { button, label } = actionParts();
	expect(modifier(button, "buttonStyle")).toEqual(["plain"]);
	// A plain button is only as tappable as its label, so the label carries the target.
	expect(modifier(label, "frame")).toEqual([{ minWidth: touchTarget, minHeight: touchTarget }]);
	expect(modifier(label, "contentShape")).toEqual([{ shape: "rectangle" }]);
	act(() => renderer!.update(<ElicitationAction label="Submit" primary onPress={vi.fn()} />));
	expect(modifier(actionParts().button, "buttonStyle")).toEqual(["borderedProminent"]);
	expect(modifier(actionParts().button, "controlSize")).toEqual(["large"]);
	expect(actionParts().label.props.modifiers).toEqual([]);
});
