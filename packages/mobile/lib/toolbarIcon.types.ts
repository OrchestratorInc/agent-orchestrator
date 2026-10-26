import type { AndroidSymbol } from "expo-symbols";
import type { ImageSourcePropType } from "react-native";
import type { SFSymbol } from "sf-symbols-typescript";
import type { AppSymbolName, IOSSymbol, symbols } from "./symbols";

/**
 * Material Symbols bundled as vector drawables in assets/icons, by their Android
 * name. Android's native header items, Compose `Icon` and MenuView actions take
 * an image source rather than a glyph name, so a symbol is only available to
 * them once its drawable is. Add one with
 * `node packages/mobile/scripts/generate-material-drawables.mjs <name>`, list it
 * here and require it in ./toolbarIcon.android.
 */
export const materialDrawableNames = [
	"add",
	"arrow_back",
	"arrow_forward",
	"chat_bubble",
	"check",
	"close",
	"delete",
	"desktop_windows",
	"devices",
	"filter_list",
	"find_in_page",
	"folder",
	"language",
	"more_vert",
	"notifications",
	"open_in_new",
	"refresh",
	"settings",
	"smartphone",
	"tab",
	"terminal",
	"vertical_align_top",
	"wrap_text",
] as const satisfies readonly AndroidSymbol[];

export type MaterialDrawableName = (typeof materialDrawableNames)[number];

/** Registry glyphs whose Android drawable is bundled, so they work as native header items on both platforms. */
export type ToolbarSymbolName = {
	[Name in AppSymbolName]: (typeof symbols)[Name]["android"] extends MaterialDrawableName ? Name : never;
}[AppSymbolName];

/** A registry name, or a one-off pair whose Android side has a bundled drawable. */
export type ToolbarSymbol = ToolbarSymbolName | { ios: IOSSymbol; android: MaterialDrawableName };

/** What the `icon` prop of `Stack.Toolbar.Button` and `Stack.Toolbar.Menu` takes. */
export type ToolbarIcon = SFSymbol | ImageSourcePropType;
