import type { ReactNode } from "react";
import type { StyleProp, ViewStyle } from "react-native";
import { LongPressMenu, type LongPressMenuAction } from "../long-press-menu";

export type MessageMenuAction = "edit" | "copy";

const EDIT: LongPressMenuAction<MessageMenuAction> = { id: "edit", title: "Edit", icon: "edit-2", systemImage: "pencil" };
const COPY: LongPressMenuAction<MessageMenuAction> = { id: "copy", title: "Copy", icon: "copy", systemImage: "doc.on.doc" };

/**
 * Long-press menu on a sent human message: Edit (when the daemon can branch
 * before it) and Copy, as the desktop shows them on hover.
 */
export function MessageContextMenu({ children, canEdit, onAction, style }: {
	children: ReactNode;
	canEdit: boolean;
	onAction(action: MessageMenuAction): void;
	style?: StyleProp<ViewStyle>;
}) {
	return (
		<LongPressMenu
			actions={canEdit ? [EDIT, COPY] : [COPY]}
			onAction={onAction}
			accessibilityHint="Long-press for Edit and Copy"
			style={style}
			testID="message-menu"
		>
			{children}
		</LongPressMenu>
	);
}
