import { symbols, type IOSSymbol } from "./symbols";
import type { MaterialDrawableName } from "./toolbarIcon.types";

export type NativeHeaderButtonIcon = "menu" | "bell" | "close" | "check" | "back" | "more" | "add" | "trash";

export type NativeHeaderButtonProps = {
	icon: NativeHeaderButtonIcon;
	/** The control's name for screen readers; it has no visible text. */
	label: string;
	onPress: () => void;
	/** Non-zero shows an unread dot. The number itself is not drawn, so name it in `label`. */
	badge?: number;
};

/**
 * Each control's glyph on each platform. Android draws from a bundled drawable,
 * so every Android name here must be one.
 */
export const nativeHeaderGlyphs: Record<NativeHeaderButtonIcon, { ios: IOSSymbol; android: MaterialDrawableName }> = {
	// The drawer opener is the only header glyph the registry does not carry.
	menu: { ios: "line.3.horizontal", android: "menu" },
	bell: symbols.notifications,
	close: symbols.close,
	check: symbols.checkmark,
	back: symbols.back,
	// The glass circle already rounds it on iOS, so the bare ellipsis; M3 stacks
	// a top app bar's overflow vertically.
	more: { ios: "ellipsis", android: "more_vert" },
	add: symbols.add,
	trash: symbols.delete,
};
