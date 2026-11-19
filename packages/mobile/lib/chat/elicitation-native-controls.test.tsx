import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, describe, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({ StyleSheet: { create: <T,>(styles: T) => styles }, View: "View" }));
vi.mock("@expo/ui/jetpack-compose", () => ({
	Button: "Button", Checkbox: "Checkbox", Column: "Column", Host: "Host", ListItem: "ListItem",
	OutlinedTextField: Object.assign(function OutlinedTextField() { return null; }, { Placeholder: "OutlinedTextField.Placeholder" }),
	RadioButton: "RadioButton", Row: "Row", Spacer: "Spacer", Text: "Text", TextButton: "TextButton", useNativeState: () => ({ get: () => "", set: vi.fn() }),
}));
// Each modifier records its name and arguments, so a test can read what a control was given.
vi.mock("@expo/ui/jetpack-compose/modifiers", () => Object.fromEntries([
	"fillMaxWidth", "selectable", "selectableGroup", "testID", "toggleable", "weight", "width",
].map((name) => [name, (...args: unknown[]) => ({ [name]: args })])));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ accent: "#0a84ff", onAccent: "#ffffff" }), useThemeState: () => ({ scheme: "light" }) }));

import { OutlinedTextField } from "@expo/ui/jetpack-compose";
import { haptics } from "../haptics";
import { ElicitationAction, ElicitationActionRow, ElicitationTextField } from "./elicitation-native-controls";

let renderer: ReactTestRenderer | undefined;
afterEach(() => { if (renderer) act(() => renderer!.unmount()); renderer = undefined; });

// The measuring copy renders first, inside the view that spans the row.
function actionParts() {
	const ruler = renderer!.root.findByType("View" as never);
	const [copyHost, visible] = renderer!.root.findAllByType("Host" as never);
	const [copy, button] = renderer!.root.findAll((node) => ["Button", "TextButton"].includes(node.type as string));
	const [, title] = renderer!.root.findAllByType("Text" as never);
	return { ruler, copyHost, visible, copy, button, title };
}
function measure(row: number, label: number) {
	act(() => {
		actionParts().ruler.props.onLayout({ nativeEvent: { layout: { width: row } } });
		actionParts().copyHost.props.onLayoutContent({ nativeEvent: { width: label, height: 48 } });
	});
}
const modifiers = (node: ReactTestInstance) => node.props.modifiers as Record<string, unknown>[] | undefined;

it("sizes a Material action to its label while that fits the row", () => {
	act(() => { renderer = create(<ElicitationAction label="Save" primary onPress={vi.fn()} />); });
	const { ruler, copy, button, title } = actionParts();
	// The copy spans the row unseen, out of reach of touch and TalkBack.
	expect(ruler.props.importantForAccessibility).toBe("no-hide-descendants");
	expect(copy.props.onClick).toBeUndefined();
	// The title wraps rather than ending in an ellipsis.
	expect(title.props.maxLines).toBeUndefined();
	expect(modifiers(button)).toBeUndefined();
	measure(360, 96);
	expect(modifiers(actionParts().button)).toBeUndefined();
});

it("holds a decision wider than its row to the row, so its label wraps", () => {
	const action = (label: string) => <ElicitationAction label={label} onPress={vi.fn()} />;
	act(() => { renderer = create(action("Yes, and don't ask again for npm test in this project")); });
	expect(actionParts().button.type).toBe("TextButton");
	expect(modifiers(actionParts().button)).toBeUndefined();
	measure(311.7, 452);
	expect(modifiers(actionParts().button)).toEqual([{ width: [311] }]);
	expect(modifiers(actionParts().copy)).toBeUndefined();
	act(() => renderer!.update(action("Sending…")));
	expect(modifiers(actionParts().button)).toBeUndefined();
});

it("taps on press unless the caller buzzes for the outcome", () => {
	const onPress = vi.fn();
	act(() => { renderer = create(<ElicitationAction label="Cancel" onPress={onPress} />); });
	act(() => actionParts().button.props.onClick());
	expect(haptics.tap).toHaveBeenCalledOnce();
	act(() => renderer!.update(<ElicitationAction label="Save" primary reportsOutcome onPress={onPress} />));
	act(() => actionParts().button.props.onClick());
	expect(haptics.tap).toHaveBeenCalledOnce();
	expect(onPress).toHaveBeenCalledTimes(2);
});

// The timeline reveals a request field when it takes focus; losing it asks nothing.
it("reports a Material text field taking focus", () => {
	const onFocus = vi.fn();
	act(() => { renderer = create(<ElicitationTextField value="" label="Title" onChange={vi.fn()} onFocus={onFocus} />); });
	const field = renderer!.root.findByType(OutlinedTextField);
	act(() => field.props.onFocusChanged(true));
	act(() => field.props.onFocusChanged(false));
	expect(onFocus).toHaveBeenCalledOnce();
});

// An approval's decisions share one line, whatever their labels or the text
// size: one native row lays them out, and a crowded row ellipsizes a label
// rather than wrapping the last decision under the rest.
describe("action row", () => {
	const decisions = () => [
		{ key: "reject", label: "Deny", onPress: vi.fn() },
		{ key: "allow", label: "Allow Once", primary: true, onPress: vi.fn() },
		{ key: "always", label: "Always Allow", onPress: vi.fn() },
	];

	it("lays every decision out in one native row, right-aligned", () => {
		act(() => { renderer = create(<ElicitationActionRow actions={decisions()} reportsOutcome />); });
		expect(renderer!.root.findAllByType("Host" as never)).toHaveLength(1);
		const row = renderer!.root.findByType("Row" as never);
		const [spacer, ...buttons] = row.children as ReactTestInstance[];
		// The spacer takes what the decisions leave, and nothing once they need it all.
		expect(spacer.type).toBe("Spacer");
		expect(modifiers(spacer)).toEqual([{ weight: [1] }]);
		expect(buttons.map((button) => button.type)).toEqual(["TextButton", "Button", "TextButton"]);
	});

	it("keeps each label on one line, ellipsized when the row runs short", () => {
		act(() => { renderer = create(<ElicitationActionRow actions={decisions()} reportsOutcome />); });
		for (const title of renderer!.root.findAllByType("Text" as never)) {
			expect(title.props).toMatchObject({ maxLines: 1, overflow: "ellipsis" });
		}
	});

	it("presses the decision tapped, buzzing only when the caller doesn't", () => {
		const actions = decisions();
		vi.mocked(haptics.tap).mockClear();
		act(() => { renderer = create(<ElicitationActionRow actions={actions} reportsOutcome />); });
		const press = (index: number) => renderer!.root.findAll((node) => ["Button", "TextButton"].includes(node.type as string))[index].props.onClick();
		act(() => press(1));
		expect(actions[1].onPress).toHaveBeenCalledOnce();
		expect(haptics.tap).not.toHaveBeenCalled();
		act(() => renderer!.update(<ElicitationActionRow actions={actions} />));
		act(() => press(0));
		expect(haptics.tap).toHaveBeenCalledOnce();
	});
});
