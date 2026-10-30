import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { Pressable } from "react-native";
import { usePressFeedback } from "./pressFeedback";
import { useTheme, useThemeState } from "./ThemeProvider";
import { workerActionDrawable } from "./worker-action-drawables";
import { workerAccessibilityActions, workerActionNamed, type WorkerActionId } from "./worker-action-model";
import type { WorkerRowContextMenuProps } from "./worker-row-interaction.types";

// The web build. MenuView draws the trigger there but fires no actions, so the
// row's menu is iOS's and Android's; this keeps the row itself working.
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
				onPress={onPress}
				style={({ pressed }) => [style, pressed && feedback.pressed]}
			>
				{children}
			</Pressable>
		</MenuView>
	);
}
