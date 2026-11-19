import type { PlatformSymbol } from "../symbols";

export type ScrollTopButtonProps = {
	onPress: () => void;
};

export const SCROLL_TOP_LABEL = "Scroll to top";

/** "To the top", not "up": SF's arrow to a line, Material's align-top. */
export const SCROLL_TOP_GLYPH = { ios: "arrow.up.to.line", android: "vertical_align_top" } as const satisfies PlatformSymbol;
