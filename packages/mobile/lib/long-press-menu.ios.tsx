import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { Pressable, View } from "react-native";
import { haptics } from "./haptics";
import type { LongPressMenuProps } from "./long-press-menu";

export type { LongPressMenuAction, LongPressMenuProps } from "./long-press-menu";

/** See long-press-menu.tsx. iOS resolves SF Symbols by name at runtime. */
export function LongPressMenu<Id extends string>({ children, actions, onAction, onPress, accessibilityLabel, accessibilityHint, style, pressedStyle, testID }: LongPressMenuProps<Id>) {
	const menuActions: MenuAction[] = actions.map((action) => ({ id: action.id, title: action.title, image: action.systemImage }));
	return (
		<MenuView
			actions={menuActions}
			shouldOpenOnLongPress
			onPressAction={(event: NativeActionEvent) => { haptics.tap(); onAction(event.nativeEvent.event as Id); }}
			testID={testID}
			style={onPress ? undefined : style}
		>
			{onPress ? (
				<Pressable
					accessibilityRole="button"
					accessibilityLabel={accessibilityLabel}
					accessibilityHint={accessibilityHint}
					accessibilityActions={actions.map((action) => ({ name: action.id, label: action.title }))}
					onAccessibilityAction={(event) => onAction(event.nativeEvent.actionName as Id)}
					onPress={onPress}
					style={({ pressed }) => [style, pressed && pressedStyle]}
				>
					{children}
				</Pressable>
			) : <View>{children}</View>}
		</MenuView>
	);
}
