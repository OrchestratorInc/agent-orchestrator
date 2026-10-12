import { useState } from "react";
import { act, create, type ReactTestRenderer } from "react-test-renderer";
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
vi.mock("@expo/ui/swift-ui/modifiers", () => Object.fromEntries([
	"buttonStyle", "controlSize", "disabled", "fixedSize", "font", "foregroundStyle", "frame",
	"multilineTextAlignment", "padding", "textFieldStyle", "tint",
].map((name) => [name, () => ({})])));
vi.mock("../glass", () => ({ glassPanel: () => ({}) }));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({}), useThemeState: () => ({ scheme: "light" }) }));

import { TextField } from "@expo/ui/swift-ui";
import { ElicitationTextField } from "./elicitation-native-controls.ios";

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
