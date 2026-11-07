import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { type ReactNode } from "react";
import { Platform, View, type StyleProp, type ViewStyle } from "react-native";
import { haptics } from "../haptics";
import { useThemeState } from "../ThemeProvider";
import { chatMenuIcon } from "./chatMenuIcons";

export type MessageMenuAction = "edit" | "copy";

/**
 * Long-press menu on a sent human message: Edit (when the daemon can branch
 * before it) and Copy, as the desktop shows them on hover. Each platform's own
 * menu: SwiftUI's context menu on iOS, Compose's dropdown on Android.
 */
export function MessageContextMenu({ children, canEdit, onAction, style }: {
	children: ReactNode;
	canEdit: boolean;
	onAction(action: MessageMenuAction): void;
	style?: StyleProp<ViewStyle>;
}) {
	const { scheme } = useThemeState();
	const items: { id: MessageMenuAction; title: string }[] = [
		...(canEdit ? [{ id: "edit" as const, title: "Edit" }] : []),
		{ id: "copy", title: "Copy" },
	];
	const actions: MenuAction[] = items.map((item) => ({ ...item, image: chatMenuIcon(item.id) }));
	const android = Platform.OS === "android";
	return (
		<MenuView
			actions={actions}
			shouldOpenOnLongPress
			colorScheme={scheme}
			// iOS lifts the bubble with a haptic of its own. Compose's menu opens
			// silently, so Android gives the long press the bump the system gives.
			onOpenMenu={() => haptics.press()}
			onPressAction={(event: NativeActionEvent) => onAction(event.nativeEvent.event as MessageMenuAction)}
			testID="message-menu"
			style={style}
		>
			{/* Android's menu trigger is no accessibility node, so the bubble itself
			    offers Edit and Copy to TalkBack. On iOS it stays a plain container:
			    grouping it would hide the image tiles inside from VoiceOver. */}
			<View
				accessible={android}
				accessibilityHint={android ? "Long-press for Edit and Copy" : undefined}
				accessibilityActions={android ? items.map((item) => ({ name: item.id, label: item.title })) : undefined}
				onAccessibilityAction={android ? (event) => onAction(event.nativeEvent.actionName as MessageMenuAction) : undefined}
			>
				{children}
			</View>
		</MenuView>
	);
}
