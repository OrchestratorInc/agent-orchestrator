import { act, create, type ReactTestRenderer } from "react-test-renderer";
import { type ReactElement, type ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

const platform = vi.hoisted(() => ({ OS: "android" }));

vi.mock("@expo/ui", async () => {
	const { createElement } = await import("react");
	const host = (name: string) => (props: { children?: ReactNode }) => createElement(name, props, props.children);
	return { FieldGroup: Object.assign(host("FieldGroup"), { Section: host("FieldGroupSection") }), Host: "Host", ListItem: "ListItem" };
});
vi.mock("@expo/ui/jetpack-compose", async () => {
	const { createElement } = await import("react");
	const host = (name: string) => (props: { children?: ReactNode }) => createElement(name, props, props.children);
	return {
		ListItem: Object.assign(host("ComposeListItem"), {
			LeadingContent: host("LeadingContent"),
			HeadlineContent: host("HeadlineContent"),
			SupportingContent: host("SupportingContent"),
			TrailingContent: host("TrailingContent"),
		}),
		RadioButton: "RadioButton",
		RNHostView: "RNHostView",
		Text: "ComposeText",
	};
});
vi.mock("@expo/ui/jetpack-compose/modifiers", () => ({
	alpha: (value: number) => ({ alpha: value }),
	selectable: (selected: boolean, handler: () => void, role: string) => ({ selectable: { selected, handler, role } }),
}));
vi.mock("@expo/ui/swift-ui", () => ({ Text: "SwiftUIText" }));
vi.mock("@expo/ui/swift-ui/modifiers", () => ({
	accessibilityAddTraits: (traits: string[]) => ({ traits }),
	disabled: (value: boolean) => ({ disabled: value }),
	font: (params: object) => ({ font: params }),
	foregroundStyle: (style: unknown) => ({ foregroundStyle: style }),
	refreshable: (handler: () => Promise<void>) => ({ refreshable: handler }),
	scrollContentBackground: (visible: string) => ({ scrollContentBackground: visible }),
}));
vi.mock("react-native", async () => {
	const { createElement } = await import("react");
	// Draws every row, with the header, empty state and footer around them.
	function FlatList(props: { data: unknown[]; renderItem(info: { item: unknown }): ReactElement; ListHeaderComponent?: ReactElement; ListEmptyComponent?: ReactElement; ListFooterComponent?: ReactElement }) {
		return createElement("FlatList", props, props.ListHeaderComponent, ...props.data.map((item) => props.renderItem({ item })), props.data.length ? null : props.ListEmptyComponent, props.ListFooterComponent);
	}
	return {
		FlatList,
		Platform: { get OS() { return platform.OS; } },
		RefreshControl: "RefreshControl",
		StyleSheet: { create: <T,>(styles: T) => styles },
		Text: "Text",
		View: "View",
	};
});
vi.mock("./AppSymbol", () => ({ AppSymbol: "AppSymbol" }));
vi.mock("./refreshControlColors", () => ({ refreshControlColors: () => ({ tintColor: "accent" }) }));
vi.mock("./ThemeProvider", () => ({
	useTheme: () => ({ accent: "accent", amber: "amber" }),
	useThemedStyles: (factory: (theme: object) => object) => factory({}),
	useThemeState: () => ({ scheme: "dark" }),
}));

import { NativePickerList, type PickerOption } from "./NativePickerList";

let renderer: ReactTestRenderer | undefined;
beforeEach(() => {
	vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
	platform.OS = "android";
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

const Logo = "Logo" as unknown as () => ReactElement;
const options: PickerOption[] = [
	{ id: "claude-code", title: "Claude Code", mark: <Logo /> },
	{ id: "codex", title: "Codex", detail: "Needs auth", warn: true },
	{ id: "aider", title: "Aider", detail: "Needs install", disabled: true },
];

function render(over: Partial<Parameters<typeof NativePickerList>[0]> = {}) {
	const handlers = { onSelect: vi.fn(), onRefresh: vi.fn(() => Promise.resolve()) };
	act(() => {
		renderer = create(<NativePickerList header={<Logo />} options={options} selected="claude-code" refreshing={false} {...handlers} {...over} />);
	});
	return handlers;
}
const byType = (type: string) => renderer!.root.findAll((node) => (node.type as unknown) === type);

describe("Android picker list", () => {
	it("draws each choice as a Material list item led by a radio button", () => {
		const { onSelect } = render();
		expect(byType("RadioButton").map((node) => node.props.selected)).toEqual([true, false, false]);
		const [first, second, third] = byType("ComposeListItem");
		expect(first.props.colors).toEqual({ containerColor: "transparent" });
		expect(second.props.modifiers[0].selectable).toMatchObject({ selected: false, role: "radioButton" });
		act(() => second.props.modifiers[0].selectable.handler());
		expect(onSelect).toHaveBeenCalledExactlyOnceWith("codex");
		// Not choosable: dimmed and not selectable.
		expect(third.props.modifiers).toEqual([{ alpha: 0.4 }]);
		expect(second.findByType("SupportingContent" as never).findByType("ComposeText" as never).props.color).toBe("amber");
		expect(first.findByType("TrailingContent" as never).findByType("RNHostView" as never).props.matchContents).toBe(true);
	});

	it("refreshes from a pull and keeps taps while the keyboard is up", () => {
		const { onRefresh } = render({ refreshing: true });
		const list = renderer!.root.findByType("FlatList" as never);
		expect(list.props.nestedScrollEnabled).toBe(true);
		expect(list.props.keyboardShouldPersistTaps).toBe("handled");
		expect(list.props.refreshControl.props.refreshing).toBe(true);
		list.props.refreshControl.props.onRefresh();
		expect(onRefresh).toHaveBeenCalledOnce();
	});

	it("says why the list is empty", () => {
		render({ options: [], empty: "No agents reported." });
		expect(renderer!.root.findAll((node) => node.props.children === "No agents reported.")).toHaveLength(1);
	});
});

describe("iOS picker list", () => {
	it("draws a Form whose current row is ticked and announced as selected", () => {
		platform.OS = "ios";
		const { onSelect } = render();
		const rows = byType("ListItem");
		expect(rows.map((row) => row.props.children)).toEqual(["Claude Code", "Codex", "Aider"]);
		expect(rows[0].props.trailing.props.name).toBe("checkmark");
		expect(rows[0].props.modifiers).toEqual([{ traits: ["isSelected"] }]);
		expect(rows[1].props.trailing).toBeUndefined();
		act(() => rows[1].props.onPress());
		expect(onSelect).toHaveBeenCalledExactlyOnceWith("codex");
		expect(rows[2].props.onPress).toBeUndefined();
		expect(rows[2].props.modifiers).toEqual([{ disabled: true }]);
		expect(rows[2].props.supportingText).toBe("Needs install");
	});

	it("refreshes when the Form is pulled down", async () => {
		platform.OS = "ios";
		const { onRefresh } = render();
		const form = renderer!.root.find((node) => (node.type as unknown) === "FieldGroup");
		const refresh = form.props.modifiers.find((modifier: { refreshable?: () => Promise<void> }) => modifier.refreshable).refreshable;
		await refresh();
		expect(onRefresh).toHaveBeenCalledOnce();
	});
});
