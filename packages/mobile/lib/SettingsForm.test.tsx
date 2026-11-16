import { createElement } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	glass: true,
	theme: { bgBase: "#base", bgElevated: "#elevated", textPrimary: "#primary", textSecondary: "#secondary", textTertiary: "#tertiary", accent: "#accent", red: "#red", green: "#green" },
}));

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles } }));
vi.mock("expo-router/react-navigation", () => ({ useHeaderHeight: () => 116 }));
vi.mock("./stackScreenOptions", () => ({ get SYSTEM_GLASS_HEADER() { return fixtures.glass; } }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("@expo/ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return {
		Host: host("Host"),
		FieldGroup: Object.assign(host("FieldGroup"), { Section: host("FieldGroupSection"), SectionHeader: host("FieldGroupSectionHeader"), SectionFooter: host("FieldGroupSectionFooter") }),
		ListItem: host("ListItem"),
	};
});
vi.mock("@expo/ui/swift-ui", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	return Object.fromEntries(["Button", "HStack", "Image", "Label", "LabeledContent", "Picker", "ProgressView", "Spacer", "Text", "Toggle", "VStack"].map((name) => [name, host(name)]));
});
vi.mock("@expo/ui/swift-ui/modifiers", () => {
	const modifier = (type: string) => (...args: unknown[]) => ({ $type: type, args });
	return Object.fromEntries(
		["accessibilityAddTraits", "accessibilityHidden", "background", "buttonBorderShape", "buttonStyle", "controlSize", "defaultScrollAnchorForRole", "disabled", "font", "foregroundStyle", "frame", "ignoreSafeArea", "imageScale", "labelsHidden", "lineLimit", "listRowBackground", "listRowInsets", "listRowSeparator", "pickerStyle", "scrollContentBackground", "tag", "tint"].map((name) => [name, modifier(name)]),
	);
});

import { SettingsButtonRow, SettingsChoiceSection, SettingsForm, SettingsLinkRow, SettingsPickerRow, SettingsSection, SettingsStatusRow, SettingsToggleRow, SettingsValueRow } from "./SettingsForm";

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
	// Measured on an iPhone: anchored for every role, each row that arrived after
	// the page opened scrolled it down by about that row, until Account sat under
	// the title, and scrolling back up was undone by the next change.
	it("opens at the top and leaves the page where it was scrolled as rows come and go", () => {
		const form = render(<SettingsForm>{null}</SettingsForm>).findByType("FieldGroup" as never);
		expect(has(form, "defaultScrollAnchorForRole", "top", "initialOffset")).toBe(true);
		expect((form.props.modifiers as Modifier[]).filter((modifier) => modifier.$type.startsWith("defaultScrollAnchor"))).toHaveLength(1);
	});

	// Measured on an iPhone: pushed from the board's Settings button, the Form
	// never received the bar's inset, so Account sat under the title and pulling
	// down sprang back to Machines. It keeps clear of the bar itself instead.
	it("keeps clear of iOS 26's clear bar itself, by the bar's height less the Form's own gap", () => {
		fixtures.glass = true;
		const titled = render(<SettingsForm><SettingsSection title="Account">{null}</SettingsSection></SettingsForm>).findByType("FieldGroup" as never);
		expect(has(titled, "ignoreSafeArea", { regions: "container", edges: "top" })).toBe(true);
		const space = (form: ReactTestInstance) => (form.props.children as React.ReactElement[])[0];
		expect(render(space(titled)).findByType("Spacer" as never).props.modifiers).toEqual(expect.arrayContaining([{ $type: "frame", args: [{ height: 116 - 53 }] }]));
		const untitled = render(<SettingsForm><SettingsSection>{null}</SettingsSection></SettingsForm>).findByType("FieldGroup" as never);
		expect(render(space(untitled)).findByType("Spacer" as never).props.modifiers).toEqual(expect.arrayContaining([{ $type: "frame", args: [{ height: 116 - 35 }] }]));

		fixtures.glass = false;
		const opaque = render(<SettingsForm><SettingsSection title="Account">{null}</SettingsSection></SettingsForm>).findByType("FieldGroup" as never);
		expect(has(opaque, "ignoreSafeArea")).toBe(false);
		expect(opaque.findAllByType("Spacer" as never)).toHaveLength(0);
		fixtures.glass = true;
	});

	it("marks a section's title as a header, leaves out a header it does not have, and keeps an empty footer slot", () => {
		const root = render(<SettingsSection title="Account" footer="Paired machines sync.">{null}</SettingsSection>);
		const header = root.findByType("FieldGroupSectionHeader" as never).findByType("Text" as never);
		expect(header.props.children).toBe("Account");
		expect(has(header, "accessibilityAddTraits", ["isHeader"])).toBe(true);
		expect(root.findByType("FieldGroupSectionFooter" as never).findByType("Text" as never).props.children).toBe("Paired machines sync.");
		const bare = render(<SettingsSection>{null}</SettingsSection>);
		expect(bare.findAllByType("FieldGroupSectionHeader" as never)).toHaveLength(0);
		// Mounted with nothing in it: a footer slot that came and went rebuilt the
		// SwiftUI section, and the field being typed in lost the keyboard (#7906).
		expect(bare.findAllByType("FieldGroupSectionFooter" as never)).toHaveLength(1);
		expect(bare.findByType("FieldGroupSectionFooter" as never).findAllByType("Text" as never)).toHaveLength(0);
	});

	it("opens a link row, reading its value but not its symbols to VoiceOver", () => {
		const onPress = vi.fn();
		const item = render(<SettingsLinkRow icon="theme" label="Theme" value="Nord" onPress={onPress} testID="theme" />).findByType("ListItem" as never);

		expect(item.props.onPress).toBe(onPress);
		const trailing = render(item.props.trailing);
		expect(trailing.findAllByType("Text" as never).map((text) => text.props.children)).toEqual(["Nord"]);
		// VoiceOver reads "Theme, Nord, button", not the symbols' names.
		expect(has(trailing.findByType("Image" as never), "accessibilityHidden", true)).toBe(true);
		expect(has(render(item.props.leading).findByType("Image" as never), "accessibilityHidden", true)).toBe(true);
	});

	it("swaps a loading value for the system spinner", () => {
		const root = render(<SettingsValueRow icon="account" label="Signed in as" value="me@example.com" loading />);
		expect(root.findAllByType("ProgressView" as never)).toHaveLength(1);
		expect(root.findByType("LabeledContent" as never).findAllByType("Text" as never)).toHaveLength(0);
	});

	it("runs an action from a button row, a destructive one carrying the role", () => {
		const onPress = vi.fn();
		const button = render(<SettingsButtonRow icon="scan" label="Scan new code" value="Dev" onPress={onPress} />).findByType("Button" as never);
		expect(button.props.role).toBeUndefined();
		expect(button.props.onPress).toBe(onPress);
		expect(has(button, "disabled")).toBe(false);
		const destructive = render(<SettingsButtonRow icon="signOut" label="Sign out" destructive onPress={vi.fn()} />).findByType("Button" as never);
		expect(destructive.props.role).toBe("destructive");
	});

	it("holds a button row while it is disabled or working", () => {
		const disabled = render(<SettingsButtonRow icon="addMachine" label="Add a machine" disabled onPress={vi.fn()} />).findByType("Button" as never);
		expect(has(disabled, "disabled", true)).toBe(true);
		const working = render(<SettingsButtonRow icon="update" label="Software update" loading onPress={vi.fn()} />);
		expect(has(working.findByType("Button" as never), "disabled", true)).toBe(true);
		expect(working.findAllByType("ProgressView" as never)).toHaveLength(1);
	});

	it("toggles with the system switch", () => {
		const onValueChange = vi.fn();
		const toggle = render(<SettingsToggleRow icon="notifications" label="Notifications" value onValueChange={onValueChange} testID="notifications" />).findByType("Toggle" as never);
		expect(toggle.props).toMatchObject({ isOn: true, onIsOnChange: onValueChange, testID: "notifications" });
		expect(toggle.findByType("Label" as never).props.title).toBe("Notifications");
	});

	// iOS keeps the menu even where Android lays the choice out as segments.
	it("picks from a menu, even when asked for segments", () => {
		const onValueChange = vi.fn();
		const options = [{ value: "light", label: "Light" }, { value: "dark", label: "Dark" }] as const;
		const picker = render(<SettingsPickerRow icon="appearance" label="Appearance" options={options} selectedValue="dark" onValueChange={onValueChange} appearance="segmented" />).findByType("Picker" as never);

		expect(picker.props.selection).toBe("dark");
		expect(has(picker, "pickerStyle", "menu")).toBe(true);
		expect(picker.findAllByType("Text" as never).map((text) => [text.props.children, (text.props.modifiers as Modifier[])[0].args[0]])).toEqual([["Light", "light"], ["Dark", "dark"]]);
		picker.props.onSelectionChange("light");
		expect(onValueChange).toHaveBeenCalledWith("light");
	});

	it("opens a status row, with its status under the name", () => {
		const onPress = vi.fn();
		const item = render(<SettingsStatusRow icon="laptop" label="Dev" status="Connected" tone="good" onPress={onPress} testID="machine-dev" />).findByType("ListItem" as never);
		expect(item.props).toMatchObject({ onPress, testID: "machine-dev" });
		expect(render(item.props.supportingText).findByType("Text" as never).props.children).toBe("Connected");
	});

	it("lists a choice section as rows, the chosen one selected for VoiceOver", () => {
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
		const rows = root.findAllByType("Button" as never);
		expect(rows.map((row) => row.props.testID)).toEqual(["themes-nord", "themes-ayu"]);
		expect(has(rows[0], "accessibilityAddTraits", ["isSelected"])).toBe(true);
		expect(has(rows[1], "accessibilityAddTraits", [])).toBe(true);
		expect(rows[0].findAllByType("Swatch" as never)).toHaveLength(1);
		rows[1].props.onPress();
		expect(onValueChange).toHaveBeenCalledWith("ayu");
	});
});
