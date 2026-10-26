import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, expect, it, vi } from "vitest";

Object.assign(globalThis, { IS_REACT_ACT_ENVIRONMENT: true });
vi.mock("react-native", () => ({ StyleSheet: { create: <T,>(styles: T) => styles }, View: "View" }));
vi.mock("@expo/ui/jetpack-compose", () => ({
	Button: "Button", Checkbox: "Checkbox", Column: "Column", Host: "Host", ListItem: "ListItem", OutlinedTextField: "OutlinedTextField",
	RadioButton: "RadioButton", Text: "Text", TextButton: "TextButton", useNativeState: () => ({ get: () => "", set: vi.fn() }),
}));
// Each modifier records its name and arguments, so a test can read what a control was given.
vi.mock("@expo/ui/jetpack-compose/modifiers", () => Object.fromEntries([
	"defaultMinSize", "fillMaxWidth", "selectable", "selectableGroup", "testID", "toggleable", "width",
].map((name) => [name, (...args: unknown[]) => ({ [name]: args })])));
vi.mock("../haptics", () => ({ haptics: { select: vi.fn(), tap: vi.fn() } }));
vi.mock("../ThemeProvider", () => ({ useTheme: () => ({ accent: "#0a84ff", onAccent: "#ffffff" }), useThemeState: () => ({ scheme: "light" }) }));

import { haptics } from "../haptics";
import { ElicitationAction } from "./elicitation-native-controls";

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
	act(() => { renderer = create(<ElicitationAction label="Save" primary width={82} onPress={vi.fn()} />); });
	const { ruler, copyHost, visible, copy, button, title } = actionParts();
	expect(button.type).toBe("Button");
	expect(button.props.colors).toEqual({ containerColor: "#0a84ff", contentColor: "#ffffff" });
	expect(visible.props.matchContents).toBe(true);
	expect(visible.props.style).toEqual({ maxWidth: "100%" });
	// The copy spans the row unseen, out of reach of touch and TalkBack.
	expect(ruler.props.pointerEvents).toBe("none");
	expect(ruler.props.importantForAccessibility).toBe("no-hide-descendants");
	expect(ruler.props.style).toMatchObject({ position: "absolute", left: 0, right: 0, opacity: 0 });
	expect(copyHost.props.matchContents).toBe(true);
	expect(copy.props.onClick).toBeUndefined();
	// The title wraps rather than ending in an ellipsis.
	expect(title.props.maxLines).toBeUndefined();
	expect(title.props.style).toMatchObject({ textAlign: "center" });
	measure(360, 96);
	expect(modifiers(actionParts().button)).toEqual([{ defaultMinSize: [{ minWidth: 82 }] }]);
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
