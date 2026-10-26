import { MenuView, type MenuAction, type NativeActionEvent } from "@expo/ui/community/menu";
import { type ReactNode } from "react";
import { View, type StyleProp, type ViewStyle } from "react-native";
import type { MessageMenuAction } from "./MessageContextMenu";

export type { MessageMenuAction } from "./MessageContextMenu";

/** See MessageContextMenu.tsx. iOS resolves SF Symbols by name at runtime. */
export function MessageContextMenu({ children, canEdit, onAction, style }: {
	children: ReactNode;
	style?: StyleProp<ViewStyle>;
	canEdit: boolean;
	onAction(action: MessageMenuAction): void;
}) {
	const actions: MenuAction[] = [
		...(canEdit ? [{ id: "edit", title: "Edit", image: "pencil" as const }] : []),
		{ id: "copy", title: "Copy", image: "doc.on.doc" as const },
	];
	return (
		<MenuView
			actions={actions}
			shouldOpenOnLongPress
			onPressAction={(event: NativeActionEvent) => onAction(event.nativeEvent.event as MessageMenuAction)}
			testID="message-menu"
			style={style}
		>
			<View>{children}</View>
		</MenuView>
	);
}
