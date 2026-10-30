import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useRef } from "react";
import { Pressable } from "react-native";
import { haptics } from "./haptics";
import { usePressFeedback } from "./pressFeedback";
import { useTheme, useThemeState } from "./ThemeProvider";
import { workerActionDrawable } from "./worker-action-drawables";
import { workerAccessibilityActions, workerActionNamed, type WorkerActionId } from "./worker-action-model";
import type { WorkerRowContextMenuProps } from "./worker-row-interaction.types";

/**
 * The row and its long-press menu: Compose's DropdownMenu, anchored to the row,
 * with each action's Material Symbol.
 *
 * MenuView listens for the long press on a Pressable of its own around the
 * trigger, and that one never sees it: the row's Pressable inside it takes the
 * touch. So the row asks the menu to open, and gives the long-press haptic the
 * system gives a view that opens a menu.
 */
export function WorkerRowContextMenu({
	children,
	onPress,
	actions,
	onAction,
	accessibilityLabel,
	accessibilityHint,
	style,
}: WorkerRowContextMenuProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const feedback = usePressFeedback("row");
	const menu = useRef<MenuComponentRef>(null);

	const menuActions: MenuAction[] = actions.map((action) => ({
		id: action.id,
		title: action.title,
		image: workerActionDrawable(action.id),
		imageColor: action.destructive ? t.red : t.textSecondary,
		titleColor: action.destructive ? t.red : t.textPrimary,
		attributes: action.destructive ? { destructive: true } : undefined,
	}));

	const chooseAction = (event: NativeActionEvent) => {
		onAction(event.nativeEvent.event as WorkerActionId);
	};

	return (
		<MenuView
			ref={menu}
			colorScheme={scheme}
			actions={menuActions}
			shouldOpenOnLongPress
			onPressAction={chooseAction}
			testID="worker-row-menu"
		>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={accessibilityLabel}
				accessibilityHint={accessibilityHint}
				accessibilityActions={workerAccessibilityActions(actions)}
				onAccessibilityAction={(event) => {
					const id = workerActionNamed(actions, event.nativeEvent.actionName);
					if (id) onAction(id);
				}}
				android_ripple={feedback.android_ripple}
				onPress={onPress}
				onLongPress={() => {
					haptics.press();
					menu.current?.show();
				}}
				style={({ pressed }) => [style, pressed && feedback.pressed]}
			>
				{children}
			</Pressable>
		</MenuView>
	);
}
