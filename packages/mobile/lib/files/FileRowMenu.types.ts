import type { ReactNode } from "react";
import type { AccessibilityActionEvent, AccessibilityActionInfo, StyleProp, ViewStyle } from "react-native";
import type { FileIconName } from "./fileIcons.types";

/** What a file row's long-press menu offers. */
export type FileRowAction = "openDiff" | "openFile" | "copyPath" | "share";

export type FileRowMenuProps = {
	/** In menu order. A file with no changes has no diff to open. */
	actions: readonly FileRowAction[];
	onAction: (action: FileRowAction) => void;
	/** The row's own tap. */
	onPress: () => void;
	accessibilityLabel: string;
	accessibilityHint: string;
	style: StyleProp<ViewStyle>;
	children: ReactNode;
};

export const fileRowActionTitle: Record<FileRowAction, string> = {
	openDiff: "Open diff",
	openFile: "Open file",
	copyPath: "Copy path",
	share: "Share",
};

export const fileRowActionIcon: Record<FileRowAction, FileIconName> = {
	openDiff: "openDiff",
	openFile: "openFile",
	copyPath: "copy",
	share: "share",
};

/**
 * The menu's actions as the row's own accessibility actions, so VoiceOver's
 * rotor and TalkBack's actions menu reach them without the long press.
 */
export function fileRowAccessibility(actions: readonly FileRowAction[], onAction: (action: FileRowAction) => void): {
	accessibilityActions: AccessibilityActionInfo[];
	onAccessibilityAction: (event: AccessibilityActionEvent) => void;
} {
	return {
		accessibilityActions: actions.map((action) => ({ name: action, label: fileRowActionTitle[action] })),
		onAccessibilityAction: (event) => {
			const name = event.nativeEvent.actionName as FileRowAction;
			if (actions.includes(name)) onAction(name);
		},
	};
}
