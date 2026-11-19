import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useEffect, useRef, useState } from "react";
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
 *
 * The menu exists only while it is open. Android pushes every scroll frame
 * into React Native's shadow tree, and each push lays out everything mounted,
 * including screens under the current one; a Compose menu under every worker
 * row was most of a resting row's nodes, so the board slowed every scroll in
 * the app.
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
	// Where the open menu sits; null while no menu exists.
	const [anchor, setAnchor] = useState<{ x: number; y: number } | null>(null);
	const teardown = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);

	// Opened once the menu has mounted with its anchor under the finger.
	useEffect(() => {
		if (!anchor) return;
		const frame = requestAnimationFrame(() => menu.current?.show());
		return () => cancelAnimationFrame(frame);
	}, [anchor]);
	useEffect(() => () => clearTimeout(teardown.current), []);

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
					clearTimeout(teardown.current);
					setAnchor({ ...touch.current });
				}}
				style={({ pressed }) => [style, pressed && feedback.pressed]}
			>
				{children}
			</Pressable>
			{anchor ? <MenuView
				ref={menu}
				colorScheme={scheme}
				actions={menuActions}
				onPressAction={chooseAction}
				onCloseMenu={() => {
					// Compose plays the close on its popup; unmounting the host
					// at once would cut it off.
					clearTimeout(teardown.current);
					teardown.current = setTimeout(() => setAnchor(null), MENU_TEARDOWN_MS);
				}}
				style={[styles.anchor, { left: anchor.x, top: anchor.y }]}
				testID="worker-row-menu"
			>
				<View pointerEvents="none" style={styles.point} />
			</MenuView> : null}
		</View>
	);
}

/** Long enough for Compose's menu exit animation to finish. */
const MENU_TEARDOWN_MS = 400;

const styles = StyleSheet.create({
	// Takes no touches: the row under it has them all.
	anchor: { position: "absolute", pointerEvents: "none" },
	point: { width: 1, height: 1 },
});
