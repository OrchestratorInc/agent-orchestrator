import type { ImageSourcePropType } from "react-native";

// Bundled, not resolved by name: a `require` of a missing file fails at bundle
// time rather than drawing an empty icon, so this map and assets/icons must
// agree exactly. Generate a new one with
// `node packages/mobile/scripts/generate-material-drawables.mjs <name>`.
const drawables = {
	chat_bubble: require("../assets/icons/chat_bubble.xml"),
	check_circle: require("../assets/icons/check_circle.xml"),
	close: require("../assets/icons/close.xml"),
	more_horiz: require("../assets/icons/more_horiz.xml"),
	open_in_new: require("../assets/icons/open_in_new.xml"),
	play_arrow: require("../assets/icons/play_arrow.xml"),
	power_settings_new: require("../assets/icons/power_settings_new.xml"),
	refresh: require("../assets/icons/refresh.xml"),
	send: require("../assets/icons/send.xml"),
	terminal: require("../assets/icons/terminal.xml"),
} satisfies Record<string, ImageSourcePropType>;

/** A Material Symbol the review screens draw through Compose, by its Android name. */
export type ReviewDrawableName = keyof typeof drawables;

/**
 * The review screens' Material Symbols as image sources, for Compose `Icon`
 * and menu rows, which take a drawable rather than a glyph name. Android only.
 */
export function reviewDrawable(name: ReviewDrawableName): ImageSourcePropType {
	return drawables[name];
}
