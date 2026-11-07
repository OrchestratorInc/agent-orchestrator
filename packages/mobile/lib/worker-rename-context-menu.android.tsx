import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useRef, useState } from "react";
import { Pressable, StyleSheet, View, type GestureResponderEvent } from "react-native";
import { haptics } from "./haptics";
import { usePressFeedback } from "./pressFeedback";
import { useTheme, useThemeState } from "./ThemeProvider";
import { workerActionDrawable } from "./worker-action-drawables";
import { workerAccessibilityActions, workerActionNamed, type WorkerActionId } from "./worker-action-model";
import type { WorkerRowContextMenuProps } from "./worker-row-interaction.types";

/**
 * The row and its long-press menu: Compose's DropdownMenu, with each action's
 * Material Symbol.
 *
 * Compose opens the menu at its anchor's start edge, and anchored to the whole
 * row it always opened against the screen's left edge, wherever the finger
 * was. The anchor is instead a point placed under the finger as the long press
 * lands, so the menu opens there, as a long press on any Android list does;
 * Compose moves it in from an edge it would cross.
 *
 * The row asks the menu to open, and gives the long-press haptic the system
 * gives a view that opens a menu.
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
	// Where the finger came down, in the row, and where the anchor sits.
	const touch = useRef({ x: 0, y: 0 });
	const [anchor, setAnchor] = useState({ x: 0, y: 0 });

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
		<View>
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
				onPressIn={(event: GestureResponderEvent) => {
					touch.current = { x: event.nativeEvent.locationX, y: event.nativeEvent.locationY };
				}}
				onLongPress={() => {
					haptics.press();
					setAnchor(touch.current);
					// Opened once the anchor has moved under the finger.
					requestAnimationFrame(() => menu.current?.show());
				}}
				style={({ pressed }) => [style, pressed && feedback.pressed]}
			>
				{children}
			</Pressable>
			<MenuView
				ref={menu}
				colorScheme={scheme}
				actions={menuActions}
				onPressAction={chooseAction}
				style={[styles.anchor, { left: anchor.x, top: anchor.y }]}
				testID="worker-row-menu"
			>
				<View pointerEvents="none" style={styles.point} />
			</MenuView>
		</View>
	);
}

const styles = StyleSheet.create({
	// Takes no touches: the row under it has them all.
	anchor: { position: "absolute", pointerEvents: "none" },
	point: { width: 1, height: 1 },
});
