import type { ImageSourcePropType } from "react-native";
import { symbols } from "./symbols";
import type { MaterialDrawableName, ToolbarIcon, ToolbarSymbol } from "./toolbarIcon.types";

export type { MaterialDrawableName, ToolbarIcon, ToolbarSymbol, ToolbarSymbolName } from "./toolbarIcon.types";

// Bundled, not resolved by name: a `require` of a missing file fails at bundle
// time rather than drawing an empty button, so this map and assets/icons must
// agree exactly, and the type keeps it complete.
const drawables: Record<MaterialDrawableName, ImageSourcePropType> = {
	add: require("../assets/icons/add.xml"),
	arrow_back: require("../assets/icons/arrow_back.xml"),
	arrow_forward: require("../assets/icons/arrow_forward.xml"),
	chat_bubble: require("../assets/icons/chat_bubble.xml"),
	check: require("../assets/icons/check.xml"),
	close: require("../assets/icons/close.xml"),
	delete: require("../assets/icons/delete.xml"),
	desktop_windows: require("../assets/icons/desktop_windows.xml"),
	devices: require("../assets/icons/devices.xml"),
	filter_list: require("../assets/icons/filter_list.xml"),
	find_in_page: require("../assets/icons/find_in_page.xml"),
	folder: require("../assets/icons/folder.xml"),
	language: require("../assets/icons/language.xml"),
	more_vert: require("../assets/icons/more_vert.xml"),
	notifications: require("../assets/icons/notifications.xml"),
	open_in_new: require("../assets/icons/open_in_new.xml"),
	refresh: require("../assets/icons/refresh.xml"),
	settings: require("../assets/icons/settings.xml"),
	smartphone: require("../assets/icons/smartphone.xml"),
	tab: require("../assets/icons/tab.xml"),
	wrap_text: require("../assets/icons/wrap_text.xml"),
};

/** A bundled Material Symbol, for Compose `Icon` and anything else that takes an image source. */
export function materialDrawable(name: MaterialDrawableName): ImageSourcePropType {
	return drawables[name];
}

/**
 * The `icon` for a native header item (`Stack.Toolbar.Button`, `.Menu`). On
 * Android those render as Compose icon buttons, which draw only an image source.
 */
export function toolbarIcon(symbol: ToolbarSymbol): ToolbarIcon {
	return drawables[typeof symbol === "string" ? symbols[symbol].android : symbol.android];
}
