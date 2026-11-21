import type { IOSSymbol } from "./symbols";
import type { MaterialDrawableName } from "./toolbarIcon.types";

/**
 * The glyphs a menu row can lead with: the SF Symbol iOS draws, and the
 * bundled Material Symbol Android draws (`materialDrawable`).
 */
export const itemActionIcons = {
	openExternal: { ios: "arrow.up.right.square", android: "open_in_new" },
	send: { ios: "paperplane", android: "send" },
	resolve: { ios: "checkmark.circle", android: "check_circle" },
	stop: { ios: "power", android: "power_settings_new" },
} as const satisfies Record<string, { ios: IOSSymbol; android: MaterialDrawableName }>;

export type ItemActionIcon = keyof typeof itemActionIcons;

export type ItemAction = {
	id: string;
	label: string;
	icon?: ItemActionIcon;
	/** Drawn in red and, on iOS, announced as destructive. */
	destructive?: boolean;
	onPress: () => void;
};

export type ItemActionsMenuProps = {
	actions: readonly ItemAction[];
	accessibilityLabel: string;
	disabled?: boolean;
	loading?: boolean;
};
