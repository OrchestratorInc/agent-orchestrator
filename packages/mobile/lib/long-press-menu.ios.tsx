import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { Pressable, View } from "react-native";
import { haptics } from "./haptics";
import type { LongPressMenuProps } from "./long-press-menu";

export type { LongPressMenuAction, LongPressMenuProps } from "./long-press-menu";

/** See long-press-menu.tsx. iOS resolves SF Symbols by name at runtime. */
export function LongPressMenu<Id extends string>({ children, actions, onAction, onPress: rowPress, accessibilityLabel, accessibilityHint, style, pressedStyle, testID, openOn = "longPress", containerStyle }: LongPressMenuProps<Id>) {
	// A "⋯" button opens the system menu on a tap: MenuView then owns the press.
	const onPress = openOn === "press" ? undefined : rowPress;
	const menuActions: MenuAction[] = actions.map((action) => ({ id: action.id, title: action.title, image: action.systemImage }));
	return (
		<MenuView
			actions={menuActions}
			shouldOpenOnLongPress={openOn !== "press"}
			onPressAction={(event: NativeActionEvent) => { haptics.tap(); onAction(event.nativeEvent.event as Id); }}
			testID={testID}
			style={onPress ? containerStyle : [style, containerStyle]}
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
			) : <View accessible={openOn === "press"} accessibilityRole={openOn === "press" ? "button" : undefined} accessibilityLabel={openOn === "press" ? accessibilityLabel : undefined}>{children}</View>}
		</MenuView>
	);
}
