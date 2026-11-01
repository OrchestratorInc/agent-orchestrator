import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";
import { createElement, type ReactNode } from "react";
import { act, create, type ReactTestInstance, type ReactTestRenderer } from "react-test-renderer";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({
	theme: { bgBase: "#base", bgElevated: "#elevated", textPrimary: "#primary", textSecondary: "#secondary", textTertiary: "#tertiary", accent: "#accent", accentTint: "#accentTint", borderDefault: "#border", red: "#red", green: "#green" },
}));

vi.mock("react-native", () => ({ StyleSheet: { create: (styles: object) => styles } }));
vi.mock("react-native-safe-area-context", () => ({ useSafeAreaInsets: () => ({ top: 0, bottom: 24, left: 0, right: 0 }) }));
vi.mock("./ThemeProvider", () => ({ useTheme: () => fixtures.theme, useThemeState: () => ({ scheme: "dark" }) }));
vi.mock("./toolbarIcon.android", () => ({ materialDrawable: (name: string) => `drawable:${name}` }));
vi.mock("@expo/ui/jetpack-compose", async () => {
	const React = await import("react");
	const host = (name: string) => ({ children, ...props }: { children?: React.ReactNode }) => React.createElement(name, props, children);
	const slots = (name: string, names: string[]) => Object.assign(host(name), Object.fromEntries(names.map((slot) => [slot, host(`${name}.${slot}`)])));
	return {
		Box: host("Box"),
		CircularProgressIndicator: host("CircularProgressIndicator"),
		Column: host("Column"),
		DropdownMenu: Object.assign(host("DropdownMenu"), { Trigger: ({ children }: { children?: React.ReactNode }) => children, Items: host("DropdownMenu.Items") }),
		DropdownMenuItem: slots("DropdownMenuItem", ["Text", "TrailingIcon"]),
		Host: host("Host"),
		Icon: host("Icon"),
		LazyColumn: host("LazyColumn"),
		ListItem: slots("ListItem", ["HeadlineContent", "SupportingContent", "LeadingContent", "TrailingContent"]),
		OutlinedButton: host("OutlinedButton"),
		RadioButton: host("RadioButton"),
		Row: host("Row"),
		SegmentedButton: slots("SegmentedButton", ["Label"]),
		SingleChoiceSegmentedButtonRow: host("SingleChoiceSegmentedButtonRow"),
		Switch: host("Switch"),
		Text: host("Text"),
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => {
	const modifier = (type: string) => (...args: unknown[]) => ({ $type: type, args });
	return {
		...Object.fromEntries(["alpha", "background", "clickable", "clip", "fillMaxSize", "fillMaxWidth", "padding", "selectable", "selectableGroup", "size", "testID", "toggleable"].map((name) => [name, modifier(name)])),
		Shapes: { RoundedCorner: (radii: unknown) => ({ roundedCorner: radii }), Circle: { circle: true } },
	};
});

import { SettingsButtonRow, SettingsChoiceSection, SettingsForm, SettingsLinkRow, SettingsPickerRow, SettingsSection, SettingsStatusRow, SettingsToggleRow, SettingsValueRow } from "./SettingsForm.android";
import { settingsIcons } from "./SettingsForm.types";
import { materialDrawableNames } from "./toolbarIcon.types";

type Modifier = { $type: string; args: unknown[] };
const modifiersOf = (node: ReactTestInstance) => (node.props.modifiers ?? []) as Modifier[];
const find = (node: ReactTestInstance, type: string) => modifiersOf(node).find((modifier) => modifier.$type === type);

let renderers: ReactTestRenderer[] = [];

function render(element: ReactNode) {
	let renderer!: ReactTestRenderer;
	act(() => { renderer = create(element as React.ReactElement); });
	renderers.push(renderer);
	return renderer.root;
}

const slot = (item: ReactTestInstance, name: string) => item.findAllByType(`ListItem.${name}` as never)[0];
const text = (node: ReactTestInstance | undefined) => node?.findAllByType("Text" as never).map((child) => child.props.children);

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

describe("the Android settings form", () => {
	// Edge to edge: the list ends above the navigation bar it scrolls under.
	it("is one lazy Material list on the page's background, clear of the navigation bar", () => {
		const root = render(<SettingsForm testID="form"><SettingsSection title="Machines" /></SettingsForm>);
		const list = root.findByType("LazyColumn" as never);
		expect(root.findByType("Host" as never).props.colorScheme).toBe("dark");
		expect(list.props.contentPadding).toEqual({ start: 16, end: 16, top: 16, bottom: 40 });
		expect(find(list, "background")?.args).toEqual(["#base"]);
		expect(find(list, "testID")?.args).toEqual(["form"]);
	});

	it("rounds a section's ends and squares the seams between its rows, skipping rows that are not there", () => {
		const root = render(
			<SettingsSection title="Machines" footer="Footer">
				<SettingsValueRow icon="laptop" label="One" />
				{null}
				<>
					<SettingsValueRow icon="laptop" label="Two" />
					<SettingsValueRow icon="laptop" label="Three" />
				</>
			</SettingsSection>,
		);
		const shapes = root.findAllByType("ListItem" as never).map((item) => find(item, "clip")?.args[0]);
		expect(shapes).toEqual([
			{ roundedCorner: { topStart: 20, topEnd: 20, bottomStart: 4, bottomEnd: 4 } },
			{ roundedCorner: 4 },
			{ roundedCorner: { topStart: 4, topEnd: 4, bottomStart: 20, bottomEnd: 20 } },
		]);
		expect(text(root)?.[0]).toBe("Machines");
		expect(text(root)?.at(-1)).toBe("Footer");
	});

	it("rounds a lone row all round", () => {
		const item = render(<SettingsSection><SettingsValueRow icon="laptop" label="Only" /></SettingsSection>).findByType("ListItem" as never);
		expect(find(item, "clip")?.args[0]).toEqual({ roundedCorner: 20 });
	});

	// The press modifier is on the ListItem itself, after the clip, so the ripple
	// covers the whole row in its shape.
	it("makes a link row the tappable ListItem, with its value underneath and no chevron", () => {
		const onPress = vi.fn();
		const item = render(<SettingsLinkRow icon="theme" label="Theme" value="Nord" onPress={onPress} testID="theme" />).findByType("ListItem" as never);
		const types = modifiersOf(item).map((modifier) => modifier.$type);
		expect(types.indexOf("clip")).toBeLessThan(types.indexOf("clickable"));
		(find(item, "clickable")!.args[0] as () => void)();
		expect(onPress).toHaveBeenCalledOnce();
		expect(text(slot(item, "HeadlineContent"))).toEqual(["Theme"]);
		expect(text(slot(item, "SupportingContent"))).toEqual(["Nord"]);
		expect(slot(item, "LeadingContent").findByType("Icon" as never).props.source).toBe("drawable:palette");
		expect(slot(item, "TrailingContent")).toBeUndefined();
		expect(item.props.colors.containerColor).toBe("#elevated");
	});

	it("leaves a value row untappable, its value in its tone", () => {
		const item = render(<SettingsValueRow icon="laptop" label="This machine" value="Unreachable" tone="bad" />).findByType("ListItem" as never);
		expect(find(item, "clickable")).toBeUndefined();
		expect(slot(item, "SupportingContent").findByType("Text" as never).props.color).toBe("#red");
	});

	it("draws an action in the accent and a destructive one in red", () => {
		const action = render(<SettingsButtonRow icon="addMachine" label="Add a machine" onPress={vi.fn()} />).findByType("ListItem" as never);
		expect(slot(action, "HeadlineContent").findByType("Text" as never).props.color).toBe("#accent");
		const destructive = render(<SettingsButtonRow icon="signOut" label="Sign out" destructive onPress={vi.fn()} />).findByType("ListItem" as never);
		expect(slot(destructive, "HeadlineContent").findByType("Text" as never).props.color).toBe("#red");
		expect(slot(destructive, "LeadingContent").findByType("Icon" as never).props.tint).toBe("#red");
	});

	it("dims a held action and takes its press away, with Material's spinner while it works", () => {
		const held = render(<SettingsButtonRow icon="addMachine" label="Add a machine" disabled onPress={vi.fn()} />).findByType("ListItem" as never);
		expect(find(held, "clickable")).toBeUndefined();
		expect(find(held, "alpha")?.args).toEqual([0.4]);
		const working = render(<SettingsButtonRow icon="update" label="Software update" loading onPress={vi.fn()} />).findByType("ListItem" as never);
		expect(find(working, "clickable")).toBeUndefined();
		expect(slot(working, "TrailingContent").findAllByType("CircularProgressIndicator" as never)).toHaveLength(1);
	});

	it("toggles from the whole row, as a switch", () => {
		const onValueChange = vi.fn();
		const item = render(<SettingsToggleRow icon="notifications" label="Notifications" value={false} onValueChange={onValueChange} />).findByType("ListItem" as never);
		const toggle = find(item, "toggleable")!;
		expect(toggle.args[0]).toBe(false);
		expect(toggle.args[2]).toEqual({ role: "switch" });
		(toggle.args[1] as () => void)();
		expect(onValueChange).toHaveBeenCalledWith(true);
		expect(slot(item, "TrailingContent").findByType("Switch" as never).props).toMatchObject({ value: false, enabled: true });
	});

	it("opens a dropdown of the choices from a menu picker row, the chosen one checked", () => {
		const onValueChange = vi.fn();
		const options = [{ value: "prod", label: "Prod" }, { value: "dev", label: "Dev" }];
		const root = render(<SettingsPickerRow icon="laptop" label="Default machine" options={options} selectedValue="dev" onValueChange={onValueChange} testID="machine" />);
		const menu = () => root.findByType("DropdownMenu" as never);
		const item = root.findByType("ListItem" as never);

		expect(menu().props.expanded).toBe(false);
		// The choice sits at the row's end in an outlined button with the
		// dropdown arrow, so the row reads as a dropdown.
		const dropdown = slot(item, "TrailingContent").findByType("OutlinedButton" as never);
		expect(text(dropdown)).toEqual(["Dev"]);
		expect(dropdown.findByType("Icon" as never).props.source).toBe("drawable:arrow_drop_down");
		expect(slot(item, "SupportingContent")).toBeUndefined();
		act(() => (find(item, "clickable")!.args[0] as () => void)());
		expect(menu().props.expanded).toBe(true);
		act(() => menu().props.onDismissRequest());
		act(() => dropdown.props.onClick());
		expect(menu().props.expanded).toBe(true);

		const items = root.findAllByType("DropdownMenuItem" as never);
		expect(items.map((entry) => text(entry))).toEqual([["Prod"], ["Dev"]]);
		expect(items.map((entry) => entry.findAllByType("DropdownMenuItem.TrailingIcon" as never).length)).toEqual([0, 1]);
		act(() => items[0].props.onClick());
		expect(onValueChange).toHaveBeenCalledWith("prod");
		expect(menu().props.expanded).toBe(false);
	});

	it("makes a status row the tappable ListItem, its status quietly underneath and no chevron", () => {
		const onPress = vi.fn();
		const item = render(<SettingsStatusRow icon="laptop" label="Dev" status="Connected" tone="good" onPress={onPress} />).findByType("ListItem" as never);
		const supporting = slot(item, "SupportingContent");
		// The colour is only in the dot; the words stay in the secondary ink.
		expect(find(supporting.findByType("Box" as never), "background")!.args[0]).toBe("#green");
		expect(supporting.findByType("Text" as never).props.color).toBe("#secondary");
		expect(slot(item, "LeadingContent").findByType("Icon" as never).props.source).toBe("drawable:laptop");
		expect(slot(item, "TrailingContent")).toBeUndefined();
		(find(item, "clickable")!.args[0] as () => void)();
		expect(onPress).toHaveBeenCalledOnce();

		const quiet = render(<SettingsStatusRow icon="laptop" label="Dev" status="Connecting…" onPress={vi.fn()} />);
		expect(find(quiet.findByType("Box" as never), "background")!.args[0]).toBe("#tertiary");
	});

	it("lays a segmented picker out as Material's single-choice segmented buttons", () => {
		const onValueChange = vi.fn();
		const options = [{ value: "light", label: "Light" }, { value: "dark", label: "Dark" }, { value: "system", label: "System" }];
		const root = render(<SettingsPickerRow icon="appearance" label="Appearance" options={options} selectedValue="system" onValueChange={onValueChange} appearance="segmented" />);
		const buttons = root.findByType("SingleChoiceSegmentedButtonRow" as never).findAllByType("SegmentedButton" as never);

		expect(buttons.map((button) => [text(button)?.[0], button.props.selected])).toEqual([["Light", false], ["Dark", false], ["System", true]]);
		act(() => buttons[1].props.onClick());
		expect(onValueChange).toHaveBeenCalledWith("dark");
		act(() => buttons[2].props.onClick());
		expect(onValueChange).toHaveBeenCalledOnce();
	});

	it("lists a choice section as radio rows in one selectable group", () => {
		const onValueChange = vi.fn();
		const root = render(
			<SettingsChoiceSection
				label="Theme"
				options={[{ value: "nord", label: "Nord", accessory: createElement("Swatch") }, { value: "ayu", label: "Ayu" }]}
				selectedValue="nord"
				onValueChange={onValueChange}
			/>,
		);
		const group = root.findAll((node) => (node.type as unknown) === "Column" && modifiersOf(node).some((modifier) => modifier.$type === "selectableGroup"));
		expect(group).toHaveLength(1);
		const rows = root.findAllByType("ListItem" as never);
		expect(rows.map((row) => find(row, "selectable")?.args.filter((arg) => typeof arg !== "function"))).toEqual([[true, "radioButton"], [false, "radioButton"]]);
		expect(rows.map((row) => row.findByType("RadioButton" as never).props)).toEqual([{ selected: true }, { selected: false }]);
		expect(slot(rows[0], "TrailingContent").findAllByType("Swatch" as never)).toHaveLength(1);
		(find(rows[1], "selectable")!.args[1] as () => void)();
		expect(onValueChange).toHaveBeenCalledWith("ayu");
	});
});

describe("settings glyphs on Android", () => {
	const form = readFileSync(fileURLToPath(new URL("./SettingsForm.android.tsx", import.meta.url)), "utf8");

	// Compose draws a bundled vector for each glyph, and the shared registry is
	// the one place a drawable is bundled (lib/toolbarIcon.test.ts checks its files).
	it.each(Object.entries(settingsIcons))("draws %s from the shared drawable registry", (_name, symbol) => {
		expect(materialDrawableNames).toContain(symbol.android);
	});

	it("resolves every row glyph through materialDrawable", () => {
		expect(form).toContain("materialDrawable(settingsIcons[icon].android)");
		expect(form).not.toContain("require(");
	});
});
