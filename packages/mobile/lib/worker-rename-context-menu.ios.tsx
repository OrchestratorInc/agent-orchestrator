import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { Pressable } from "react-native";
import { usePressFeedback } from "./pressFeedback";
import { workerAccessibilityActions, workerActionNamed, workerActionSymbol, type WorkerActionId } from "./worker-action-model";
import type { WorkerRowContextMenuProps } from "./worker-row-interaction.types";

/** The row and its long-press menu: SwiftUI's context menu, lifting the row. */
export function WorkerRowContextMenu({
	children,
	onPress,
	actions,
	onAction,
	accessibilityLabel,
	accessibilityHint,
	style,
}: WorkerRowContextMenuProps) {
	const feedback = usePressFeedback("row");
	// iOS resolves SF Symbols by name at runtime, so every action can carry one.
	const menuActions: MenuAction[] = actions.map((action) => ({
		id: action.id,
		title: action.title,
		image: workerActionSymbol(action.id).ios,
		attributes: action.destructive ? { destructive: true } : undefined,
	}));

	const chooseAction = (event: NativeActionEvent) => {
		onAction(event.nativeEvent.event as WorkerActionId);
	};

	return (
		<MenuView
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
