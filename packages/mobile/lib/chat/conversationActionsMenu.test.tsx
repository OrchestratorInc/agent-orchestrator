import { Children, isValidElement, type ReactElement, type ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";

const fixtures = vi.hoisted(() => ({ platform: { OS: "ios" } }));

vi.mock("react-native", () => ({ Platform: fixtures.platform }));
vi.mock("expo-router", () => {
	const Menu = Object.assign(() => null, { displayName: "StackToolbarMenu" });
	const MenuAction = Object.assign(() => null, { displayName: "StackToolbarMenuAction" });
	return { Stack: { Toolbar: { Menu, MenuAction } } };
});
vi.mock("../toolbarIcon", () => ({ toolbarIcon: (name: string) => `toolbar:${name}` }));

import { Stack } from "expo-router";
import { conversationActionsMenu } from "./conversationActionsMenu";
import { conversationMenuGroups, type ConversationMenuState } from "./conversationMenuModel";

const state: ConversationMenuState = {
	canRename: true,
	canPin: true,
	canCompact: true,
	canReloadMcp: true,
	canDelete: true,
	hasUsage: true,
	pinned: false,
	openingShell: false,
	refreshing: false,
	compacting: false,
	mcpReloading: false,
	turnInFlight: true,
	interfaceSupported: true,
	interfaceSwitching: false,
};

type Props = Record<string, unknown> & { children?: ReactNode };
const children = (element: ReactElement<Props>) => Children.toArray(element.props.children).filter(isValidElement) as ReactElement<Props>[];

beforeEach(() => {
	fixtures.platform.OS = "ios";
});

describe("conversation actions menu", () => {
	it("is a header menu whose sections after the first are inline groups", () => {
		const menu = conversationActionsMenu(conversationMenuGroups(state), () => {}) as ReactElement<Props>;
		expect(menu.type).toBe(Stack.Toolbar.Menu);
		expect(menu.props).toMatchObject({ icon: "toolbar:more", accessibilityLabel: "Conversation actions" });
		const top = children(menu);
		// The first section sits directly in the menu, so Android draws no divider above it.
		expect(top.slice(0, 4).map((item) => item.type)).toEqual(Array(4).fill(Stack.Toolbar.MenuAction));
		const groups = top.slice(4);
		expect(groups.map((group) => [group.type, group.props.inline])).toEqual([
			[Stack.Toolbar.Menu, true],
			[Stack.Toolbar.Menu, true],
			[Stack.Toolbar.Menu, true],
		]);
		const archive = children(groups[2])[0];
		expect(archive.props).toMatchObject({ children: "Archive session", destructive: true, icon: "archivebox" });
	});

	it("passes each item's state through and runs its action when chosen", () => {
		const onSelect = vi.fn();
		const menu = conversationActionsMenu(conversationMenuGroups(state), onSelect) as ReactElement<Props>;
		const items = children(menu).flatMap((entry) => entry.type === Stack.Toolbar.Menu ? children(entry) : [entry]);
		const compact = items.find((item) => item.props.children === "Compact history")!;
		expect(compact.props.disabled).toBe(true);
		const history = items.find((item) => item.props.children === "Conversation history")!;
		(history.props.onPress as () => void)();
		expect(onSelect).toHaveBeenCalledWith("map");
	});

	// Android's menu items draw only bundled images and drop an SF name.
	it("leaves item glyphs to iOS", () => {
		fixtures.platform.OS = "android";
		const menu = conversationActionsMenu(conversationMenuGroups(state), () => {}) as ReactElement<Props>;
		const items = children(menu).flatMap((entry) => entry.type === Stack.Toolbar.Menu ? children(entry) : [entry]);
		expect(items.length).toBeGreaterThan(0);
		for (const item of items) expect(item.props.icon).toBeUndefined();
	});
});
