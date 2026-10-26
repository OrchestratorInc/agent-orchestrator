import { createElement } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: { bgBase: "#base", bgElevated: "#elevated", textPrimary: "#primary", textSecondary: "#secondary", textTertiary: "#tertiary", accent: "#accent", red: "#red", green: "#green" },
}));

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("Host"),
		FieldGroup: Object.assign(host("FieldGroup"), { Section: host("FieldGroupSection"), SectionFooter: host("FieldGroupSectionFooter") }),
		ListItem: host("ListItem"),
	};
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return Object.fromEntries(["Button", "HStack", "Image", "Label", "LabeledContent", "Picker", "ProgressView", "Spacer", "Text", "Toggle"].map((name) => [name, host(name)]));
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const modifier = (type: string) => (...args: unknown[]) => ({ $type: type, args });
	return Object.fromEntries(
		["accessibilityHidden", "background", "buttonStyle", "disabled", "font", "foregroundStyle", "labelsHidden", "lineLimit", "listRowBackground", "pickerStyle", "scrollContentBackground", "tag", "tint"].map((name) => [name, modifier(name)]),
	);
});

import { SettingsButtonRow, SettingsChoiceSection, SettingsForm, SettingsLinkRow, SettingsPickerRow, SettingsSection, SettingsToggleRow, SettingsValueRow } from "./SettingsForm";

let renderers: ReactTestRenderer[] = [];

/** Renders a row, or a slot a row passed on (a label, an accessory), to read it. */
function render(element: React.ReactElement) {
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(element); });
	renderers.push(renderer);
	return renderer.root;
}

type Modifier = { $type: string; args: unknown[] };
const has = (node: ReactTestInstance, type: string, ...args: unknown[]) =>
	(node.props.modifiers as Modifier[] | undefined)?.some((modifier) => modifier.$type === type && (args.length === 0 || JSON.stringify(modifier.args) === JSON.stringify(args))) ?? false;

beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	const error = console.error;
	vi.spyOn(console, "error").mockImplementation((...args) => {
		if (String(args[0]).startsWith("react-test-renderer is deprecated")) return;
		error(...args);
	});
});

afterEach(() => {
	act(() => { for (const renderer of renderers) renderer.unmount(); });
	renderers = [];
	vi.restoreAllMocks();
	vi.unstubAllGlobals();
});

describe("the iOS settings form", () => {
	// SwiftUI's Form, in the app's palette and family, scaled with Dynamic Type.
	it("is a SwiftUI Form filling its host, on the page's background", () => {
		const root = render(<SettingsForm testID="form"><SettingsSection title="Machines" /></SettingsForm>);
		const host = root.findByType("Host" as never);
		const form = root.findByType("FieldGroup" as never);

		expect(host.props.colorScheme).toBe("dark");
		expect(form.props.testID).toBe("form");
		expect(has(form, "scrollContentBackground", "hidden")).toBe(true);
		expect(has(form, "background", "#base")).toBe(true);
		expect(has(form, "font", { family: "Geist_400Regular", textStyle: "body" })).toBe(true);
	});

	it("titles sections natively and explains them in a footnote footer", () => {
		const root = render(<SettingsSection title="Account" footer="Paired machines sync.">{null}</SettingsSection>);
		expect(root.findByType("FieldGroupSection" as never).props.title).toBe("Account");
		const footer = root.findByType("FieldGroupSectionFooter" as never).findByType("Text" as never);
		expect(footer.props.children).toBe("Paired machines sync.");
		expect(has(footer, "font", { family: "Geist_400Regular", textStyle: "footnote" })).toBe(true);
		expect(has(footer, "foregroundStyle", "#secondary")).toBe(true);
	});

	it("draws a link row as a list button with its value and the disclosure chevron", () => {
		const onPress = vi.fn();
		const item = render(<SettingsLinkRow icon="theme" label="Theme" value="Nord" onPress={onPress} testID="theme" />).findByType("ListItem" as never);

		expect(item.props.onPress).toBe(onPress);
		expect(item.props.testID).toBe("theme");
		// The list's own button style gives the row its highlight.
		expect(has(item, "buttonStyle", "automatic")).toBe(true);
		expect(has(item, "listRowBackground", "#elevated")).toBe(true);
		expect(item.props.leading.props.icon).toBe("theme");
		const trailing = render(item.props.trailing);
		expect(trailing.findAllByType("Text" as never).map((text) => text.props.children)).toEqual(["Nord"]);
		const chevron = trailing.findByType("Image" as never);
		expect(chevron.props.systemName).toBe("chevron.forward");
		// VoiceOver reads "Theme, Nord, button", not the symbols' names.
		expect(has(chevron, "accessibilityHidden", true)).toBe(true);
		expect(has(render(item.props.leading).findByType("Image" as never), "accessibilityHidden", true)).toBe(true);
	});

	it("leads every row with its SF Symbol in the secondary ink, scaled with the text", () => {
		const root = render(<SettingsValueRow icon="status" label="Status" value="Connected" tone="good" />);
		const content = root.findByType("LabeledContent" as never);
		const label = render(content.props.label).findByType("Label" as never);
		expect(label.props.title).toBe("Status");
		const icon = render(label.props.icon).findByType("Image" as never);
		expect(icon.props).toMatchObject({ systemName: "dot.radiowaves.left.and.right", color: "#secondary" });
		expect(has(icon, "font", { textStyle: "body" })).toBe(true);
	});

	it.each([
		["good", "#green"],
		["bad", "#red"],
		["default", "#secondary"],
	] as const)("shows a %s value in its tone", (tone, color) => {
		const root = render(<SettingsValueRow icon="status" label="Status" value="Value" tone={tone} />);
		const value = root.findByType("LabeledContent" as never).findByType("Text" as never);
		expect(value.props.children).toBe("Value");
		expect(has(value, "foregroundStyle", color)).toBe(true);
	});

	it("swaps a loading value for the system spinner", () => {
		const root = render(<SettingsValueRow icon="account" label="Orchestrator Cloud" value="me@example.com" loading />);
		expect(root.findAllByType("ProgressView" as never)).toHaveLength(1);
		expect(root.findByType("LabeledContent" as never).findAllByType("Text" as never)).toHaveLength(0);
	});

	it("runs an action from a tinted button row", () => {
		const onPress = vi.fn();
		const button = render(<SettingsButtonRow icon="testConnection" label="Test connection" value="Connected" tone="good" onPress={onPress} />).findByType("Button" as never);
		expect(button.props.role).toBeUndefined();
		expect(button.props.onPress).toBe(onPress);
		expect(has(button, "tint", "#accent")).toBe(true);
		expect(has(button, "disabled")).toBe(false);
	});

	it("marks a destructive action with the role and red", () => {
		const root = render(<SettingsButtonRow icon="signOut" label="Sign out" destructive onPress={vi.fn()} />);
		const button = root.findByType("Button" as never);
		expect(button.props.role).toBe("destructive");
		expect(has(button, "tint", "#red")).toBe(true);
		expect(root.findByType("Label" as never).props.title).toBe("Sign out");
	});

	it("holds a button row while it is disabled or working", () => {
		const disabled = render(<SettingsButtonRow icon="testConnection" label="Test connection" disabled onPress={vi.fn()} />).findByType("Button" as never);
		expect(has(disabled, "disabled", true)).toBe(true);
		const working = render(<SettingsButtonRow icon="update" label="Software update" loading onPress={vi.fn()} />);
		expect(has(working.findByType("Button" as never), "disabled", true)).toBe(true);
		expect(working.findAllByType("ProgressView" as never)).toHaveLength(1);
	});

	// The switch keeps the system's green; the app's accent is hue-less.
	it("toggles with the system switch in its own colours", () => {
		const onValueChange = vi.fn();
		const toggle = render(<SettingsToggleRow icon="notifications" label="Notifications" value onValueChange={onValueChange} testID="notifications" />).findByType("Toggle" as never);
		expect(toggle.props).toMatchObject({ isOn: true, onIsOnChange: onValueChange, testID: "notifications" });
		expect(has(toggle, "tint")).toBe(false);
		expect(toggle.findByType("Label" as never).props.title).toBe("Notifications");
	});

	it("picks from a menu with its value in the secondary ink, on every appearance", () => {
		const onValueChange = vi.fn();
		const options = [{ value: "light", label: "Light" }, { value: "dark", label: "Dark" }] as const;
		const picker = render(<SettingsPickerRow icon="appearance" label="Appearance" options={options} selectedValue="dark" onValueChange={onValueChange} appearance="segmented" />).findByType("Picker" as never);

		expect(picker.props.selection).toBe("dark");
		expect(has(picker, "pickerStyle", "menu")).toBe(true);
		expect(has(picker, "tint", "#secondary")).toBe(true);
		expect(picker.findAllByType("Text" as never).map((text) => [text.props.children, (text.props.modifiers as Modifier[])[0].args[0]])).toEqual([["Light", "light"], ["Dark", "dark"]]);
		picker.props.onSelectionChange("light");
		expect(onValueChange).toHaveBeenCalledWith("light");
	});

	it("lists a choice section as an inline picker, each option with its accessory", () => {
		const onValueChange = vi.fn();
		const root = render(
			<SettingsChoiceSection
				label="Theme"
				options={[{ value: "nord", label: "Nord", accessory: createElement("Swatch") }, { value: "ayu", label: "Ayu" }]}
				selectedValue="nord"
				onValueChange={onValueChange}
				testID="themes"
			/>,
		);
		const picker = root.findByType("Picker" as never);
		expect(picker.props).toMatchObject({ label: "Theme", selection: "nord", testID: "themes" });
		expect(has(picker, "pickerStyle", "inline")).toBe(true);
		expect(has(picker, "labelsHidden")).toBe(true);
		const options = picker.findAllByType("HStack" as never);
		expect(options.map((option) => (option.props.modifiers as Modifier[])[0])).toEqual([{ $type: "tag", args: ["nord"] }, { $type: "tag", args: ["ayu"] }]);
		expect(options[0].findAllByType("Swatch" as never)).toHaveLength(1);
		picker.props.onSelectionChange("ayu");
		expect(onValueChange).toHaveBeenCalledWith("ayu");
	});
});
