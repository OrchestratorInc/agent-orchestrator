import { MenuView, type MenuAction } from "@expo/ui/community/menu";
import { Pressable, useWindowDimensions } from "react-native";
import { usePressFeedback } from "../pressFeedback";
import { fileIconSymbols } from "./fileIcons.types";
import { fileRowAccessibility, fileRowActionIcon, fileRowActionTitle, type FileRowAction, type FileRowMenuProps } from "./FileRowMenu.types";

export { fileRowAccessibility, fileRowActionTitle, type FileRowAction, type FileRowMenuProps } from "./FileRowMenu.types";

/**
 * A file row with the system's context menu on a long press: the row lifts,
 * and Open diff, Open file, Copy path and Share sit under it. A tap still
 * opens the row. Android's is ./FileRowMenu.android.
 */
export function FileRowMenu({ actions, onAction, onPress, accessibilityLabel, accessibilityHint, style, children }: FileRowMenuProps) {
	// The menu hosts the row in SwiftUI, which sizes it to its content; the row
	// spans the list, so it is given the width it would otherwise stretch to.
	const { width } = useWindowDimensions();
	const feedback = usePressFeedback("row");
	const menuActions: MenuAction[] = actions.map((action) => ({
		id: action,
		title: fileRowActionTitle[action],
		image: fileIconSymbols[fileRowActionIcon[action]].ios,
	}));
	return (
		<MenuView actions={menuActions} shouldOpenOnLongPress onPressAction={(event) => onAction(event.nativeEvent.event as FileRowAction)}>
			<Pressable
				accessibilityRole="button"
				accessibilityLabel={accessibilityLabel}
				accessibilityHint={accessibilityHint}
				{...fileRowAccessibility(actions, onAction)}
				onPress={onPress}
				style={({ pressed }) => [style, { width }, pressed && feedback.pressed]}
			>
				{children}
			</Pressable>
		</MenuView>
	);
}
