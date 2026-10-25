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
	check: require("../assets/icons/check.xml"),
	close: require("../assets/icons/close.xml"),
	delete: require("../assets/icons/delete.xml"),
	menu: require("../assets/icons/menu.xml"),
	more_vert: require("../assets/icons/more_vert.xml"),
	notifications: require("../assets/icons/notifications.xml"),
	open_in_new: require("../assets/icons/open_in_new.xml"),
	settings: require("../assets/icons/settings.xml"),
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
