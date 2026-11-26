import { MenuView, type MenuAction, type MenuComponentRef, type NativeActionEvent } from "@expo/ui/community/menu";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Platform, Pressable, StyleSheet, View, type GestureResponderEvent, type StyleProp, type ViewStyle } from "react-native";
import { haptics } from "../haptics";
import { useThemeState } from "../ThemeProvider";
import { chatMenuIcon } from "./chatMenuIcons";

export type MessageMenuAction = "edit" | "copy";

type MessageContextMenuProps = {
	children: ReactNode;
	canEdit: boolean;
	onAction(action: MessageMenuAction): void;
	style?: StyleProp<ViewStyle>;
};

/**
 * Long-press menu on a sent human message: Edit (when the daemon can branch
 * before it) and Copy, as the desktop shows them on hover. Each platform's own
 * menu: SwiftUI's context menu on iOS, Compose's dropdown on Android.
 */
export function MessageContextMenu(props: MessageContextMenuProps) {
	return Platform.OS === "android" ? <AndroidMessageMenu {...props} /> : <SystemMessageMenu {...props} />;
}

function menuItems(canEdit: boolean): { id: MessageMenuAction; title: string }[] {
	return [
		...(canEdit ? [{ id: "edit" as const, title: "Edit" }] : []),
		{ id: "copy", title: "Copy" },
	];
}

/** iOS: the bubble is the menu's own trigger, which lifts it with a haptic of its own. */
function SystemMessageMenu({ children, canEdit, onAction, style }: MessageContextMenuProps) {
	const { scheme } = useThemeState();
	const items = menuItems(canEdit);
	const actions: MenuAction[] = items.map((item) => ({ ...item, image: chatMenuIcon(item.id) }));
	return (
		<MenuView
			actions={actions}
			shouldOpenOnLongPress
			colorScheme={scheme}
			onOpenMenu={() => haptics.press()}
			onPressAction={(event: NativeActionEvent) => onAction(event.nativeEvent.event as MessageMenuAction)}
			testID="message-menu"
			style={style}
		>
			{/* A plain container: grouping it would hide the image tiles inside from VoiceOver. */}
			<View accessible={false}>{children}</View>
		</MenuView>
	);
}

/**
 * Android: Compose's dropdown, which exists only while it is open, anchored
 * under the finger (as the board's rows do; see WorkerRowContextMenu).
 *
 * A Compose host around every sent message measured itself again at every
 * layout in the window and redrew with the list: while a reply streamed, the
 * prompts above it were a third of the UI thread's work.
 */
function AndroidMessageMenu({ children, canEdit, onAction, style }: MessageContextMenuProps) {
	const { scheme } = useThemeState();
	const items = menuItems(canEdit);
	const menu = useRef<MenuComponentRef>(null);
	const wrapper = useRef<View>(null);
	const touch = useRef({ x: 0, y: 0 });
	const [anchor, setAnchor] = useState<{ x: number; y: number } | null>(null);
	const teardown = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => {
		if (!anchor) return;
		const frame = requestAnimationFrame(() => menu.current?.show());
		return () => cancelAnimationFrame(frame);
	}, [anchor]);
	useEffect(() => () => clearTimeout(teardown.current), []);
	return (
		<View ref={wrapper} style={style}>
			{/* The menu's trigger is no accessibility node, so the bubble itself offers Edit and Copy to TalkBack. */}
			<Pressable
				accessible
				accessibilityHint="Long-press for Edit and Copy"
				accessibilityActions={items.map((item) => ({ name: item.id, label: item.title }))}
				onAccessibilityAction={(event) => onAction(event.nativeEvent.actionName as MessageMenuAction)}
				onPressIn={(event: GestureResponderEvent) => {
					// The anchor sits in this wrapper's space. Android gives locationX/Y
					// in the space of the view under the finger, which can be the bubble's
					// text, so the touch's page position is rebased onto the wrapper.
					const { pageX, pageY, locationX, locationY } = event.nativeEvent;
					touch.current = { x: locationX, y: locationY };
					wrapper.current?.measure((_x, _y, _width, _height, left, top) => {
						touch.current = { x: pageX - left, y: pageY - top };
					});
				}}
				onLongPress={() => {
					// The bump the system gives a long press that opens a menu.
					haptics.press();
					clearTimeout(teardown.current);
					setAnchor({ ...touch.current });
				}}
				testID="message-menu-trigger"
			>
				{children}
			</Pressable>
			{anchor ? <MenuView
				ref={menu}
				colorScheme={scheme}
				actions={items.map((item) => ({ ...item, image: chatMenuIcon(item.id) }))}
				onPressAction={(event: NativeActionEvent) => onAction(event.nativeEvent.event as MessageMenuAction)}
				onCloseMenu={() => {
					// Compose plays the close on its popup; unmounting the host at once would cut it off.
					clearTimeout(teardown.current);
					teardown.current = setTimeout(() => setAnchor(null), MENU_TEARDOWN_MS);
				}}
				style={[styles.anchor, { left: anchor.x, top: anchor.y }]}
				testID="message-menu"
			>
				<View pointerEvents="none" style={styles.point} />
			</MenuView> : null}
		</View>
	);
}

/** Long enough for Compose's menu exit animation to finish. */
const MENU_TEARDOWN_MS = 400;

const styles = StyleSheet.create({
	// Takes no touches: the bubble under it has them all.
	anchor: { position: "absolute", pointerEvents: "none" },
	point: { width: 1, height: 1 },
});
