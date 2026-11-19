import { MenuView, type MenuAction, type MenuComponentRef } from "@expo/ui/community/menu";
import { useRef } from "react";
import { Pressable, useWindowDimensions } from "react-native";
import { haptics } from "../haptics";
import { usePressFeedback } from "../pressFeedback";
import { useTheme, useThemeState } from "../ThemeProvider";
import { fileIcon } from "./fileIcons";
import { fileRowAccessibility, fileRowActionIcon, fileRowActionTitle, type FileRowAction, type FileRowMenuProps } from "./FileRowMenu.types";

export { fileRowAccessibility, fileRowActionTitle, type FileRowAction, type FileRowMenuProps } from "./FileRowMenu.types";

/**
 * A file row whose long press drops Material's menu down from it. The row's
 * own Pressable takes both the tap and the long press: the menu's trigger
 * wraps it, and a touch never reaches past the innermost responder, so the
 * long press opens the menu through its handle.
 */
export function FileRowMenu({ actions, onAction, onPress, accessibilityLabel, accessibilityHint, style, children }: FileRowMenuProps) {
	const t = useTheme();
	const { scheme } = useThemeState();
	const { width } = useWindowDimensions();
	const feedback = usePressFeedback("row");
	const menu = useRef<MenuComponentRef>(null);
	const menuActions: MenuAction[] = actions.map((action) => ({
		id: action,
		title: fileRowActionTitle[action],
		image: fileIcon(fileRowActionIcon[action]),
		imageColor: t.textSecondary,
		titleColor: t.textPrimary,
	}));
	return (
		<MenuView ref={menu} colorScheme={scheme} actions={menuActions} shouldOpenOnLongPress onPressAction={(event) => onAction(event.nativeEvent.event as FileRowAction)}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={accessibilityLabel}
				accessibilityHint={accessibilityHint}
				{...fileRowAccessibility(actions, onAction)}
				onPress={onPress}
				onLongPress={() => {
					haptics.press();
					menu.current?.show();
				}}
				android_ripple={feedback.android_ripple}
				style={[style, { width }]}
			>
				{children}
			</Pressable>
		</MenuView>
	);
}
