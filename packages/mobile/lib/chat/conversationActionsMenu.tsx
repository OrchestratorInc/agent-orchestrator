import { Stack } from "expo-router";
import type { ReactElement } from "react";
import { Platform } from "react-native";
import { toolbarIcon } from "../toolbarIcon";
import type { ConversationMenuAction, ConversationMenuGroup, ConversationMenuItem } from "./conversationMenuModel";

/**
 * The conversation's "⋯" as a native pull-down menu: a UIMenu from the bar
 * button on iOS, Compose's DropdownMenu from the top app bar on Android. Each
 * section after the first is an inline submenu, which both platforms draw as a
 * group under a separator.
 *
 * A function rather than a component: Stack.Toolbar reads its children by
 * element type, so what it is handed has to be the Stack.Toolbar.Menu itself.
 * Memoise the result on what the menu shows; every new element registers the
 * header's options again, and a rebuild while the menu is open can close it.
 */
export function conversationActionsMenu(groups: readonly ConversationMenuGroup[], onSelect: (action: ConversationMenuAction) => void): ReactElement {
	const [first, ...rest] = groups;
	const action = (item: ConversationMenuItem) => (
		<Stack.Toolbar.MenuAction
			key={item.action}
			// Android menu items draw only bundled images, and an SF name is
			// dropped there; its overflow menus are text only anyway.
			icon={Platform.OS === "ios" ? item.icon : undefined}
			subtitle={item.subtitle}
			disabled={item.disabled}
			destructive={item.destructive}
			onPress={() => onSelect(item.action)}
		>
			{item.label}
		</Stack.Toolbar.MenuAction>
	);
	return (
		<Stack.Toolbar.Menu icon={toolbarIcon("more")} accessibilityLabel="Conversation actions">
			{first?.items.map(action)}
			{rest.map((group) => (
				<Stack.Toolbar.Menu key={group.title} inline>
					{group.items.map(action)}
				</Stack.Toolbar.Menu>
			))}
		</Stack.Toolbar.Menu>
	);
}
