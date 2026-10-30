import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("react-native", () => ({
	StyleSheet: { create: (styles: object) => styles },
	Text: "Text",
	View: "View",
}));
vi.mock("@expo/ui", () => ({ Host: "Host", Switch: "Switch" }));
vi.mock("../ThemeProvider", () => ({
	useThemedStyles: (factory: (theme: object) => object) => factory(new Proxy({}, { get: (_, key) => typeof key === "string" ? `theme.${key}` : undefined })),
	useThemeState: () => ({ scheme: "dark" }),
}));
vi.mock("./elicitation-native-controls", () => ({ ElicitationChoiceList: "ElicitationChoiceList", ElicitationTextField: "ElicitationTextField" }));

import { ElicitationInputField } from "./ElicitationInputField";
import type { InputProperty } from "./types";

let renderer: ReactTestRenderer | undefined;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => renderer?.unmount());
	renderer = undefined;
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

function render(property: InputProperty, value: unknown, onChange = vi.fn(), required = true) {
	act(() => { renderer = create(<ElicitationInputField name="run_tests" property={property} required={required} value={value} onChange={onChange} />); });
	return onChange;
}

describe("a yes/no field", () => {
	const row = () => renderer!.root.find((node) => node.props.accessibilityRole === "switch");

	// The label and the switch are one control to a screen reader, as a native
	// toggle row is, and it announces its state rather than showing it only.
	it("reads as one labelled switch with its state", () => {
		render({ type: "boolean" }, true);
		expect(row().props).toMatchObject({ accessible: true, accessibilityLabel: "Run tests *", accessibilityState: { checked: true } });
		act(() => renderer!.update(<ElicitationInputField name="run_tests" property={{ type: "boolean", title: "Run the suite" }} required={false} value={undefined} onChange={vi.fn()} />));
		expect(row().props).toMatchObject({ accessibilityLabel: "Run the suite", accessibilityState: { checked: false } });
	});

	it("toggles from a screen reader's activate action", () => {
		const onChange = render({ type: "boolean" }, false);
		act(() => row().props.onAccessibilityAction({ nativeEvent: { actionName: "activate" } }));
		expect(onChange).toHaveBeenLastCalledWith(true);
		act(() => row().props.onAccessibilityAction({ nativeEvent: { actionName: "magicTap" } }));
		expect(onChange).toHaveBeenCalledOnce();
		expect(row().props.accessibilityActions).toEqual([{ name: "activate" }]);
	});

	// The hosted switch has no label of its own, so it is hidden behind the row.
	it("hosts the native switch, hidden from screen readers and toggled by touch", () => {
		const onChange = render({ type: "boolean" }, true);
		const host = renderer!.root.findByType("Host" as never);
		expect(host.props).toMatchObject({ colorScheme: "dark", style: { width: 54, height: 34 } });
		expect(host.parent!.props).toMatchObject({ accessibilityElementsHidden: true, importantForAccessibility: "no-hide-descendants" });
		const nativeSwitch = host.findByType("Switch" as never);
		expect(nativeSwitch.props.value).toBe(true);
		act(() => nativeSwitch.props.onValueChange(false));
		expect(onChange).toHaveBeenLastCalledWith(false);
	});
});

describe("other fields", () => {
	it("offers a schema's options as a choice list, toggling within a multi-select", () => {
		const options = [{ const: "unit", title: "Unit" }, { const: "e2e", title: "End to end" }];
		const onChange = render({ type: "array", items: { anyOf: options } }, ["unit"]);
		const list = renderer!.root.findByType("ElicitationChoiceList" as never);
		expect(list.props.multi).toBe(true);
		expect(list.props.selected("unit")).toBe(true);
		act(() => list.props.onChange("e2e"));
		expect(onChange).toHaveBeenLastCalledWith(["unit", "e2e"]);
	});

	it("takes anything else as text, numeric for a number", () => {
		render({ type: "integer", maxLength: 4 }, 3);
		expect(renderer!.root.findByType("ElicitationTextField" as never).props).toMatchObject({ label: "Run tests *", numeric: true, maxLength: 4, value: 3 });
	});
});
