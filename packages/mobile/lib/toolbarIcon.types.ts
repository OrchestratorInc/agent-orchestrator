import type { AndroidSymbol } from "expo-symbols";
import type { ImageSourcePropType } from "react-native";
import type { SFSymbol } from "sf-symbols-typescript";
import type { AppSymbolName, IOSSymbol, symbols } from "./symbols";

/**
 * Material Symbols bundled as vector drawables in assets/icons, by their Android
 * name: the app's one drawable registry. Android's native header items, Compose
 * `Icon` and MenuView actions take an image source rather than a glyph name, so
 * a symbol is only available to them once its drawable is, and every Android
 * surface takes it from `materialDrawable` in ./toolbarIcon.android. Add one with
 * `node packages/mobile/scripts/generate-material-drawables.mjs <name>`, list it
 * here and require it in ./toolbarIcon.android.
 */
export const materialDrawableNames = [
	"account_circle",
	"add",
	"add_comment",
	"archive",
	"arrow_back",
	"arrow_downward",
	"arrow_drop_down",
	"arrow_forward",
	"arrow_upward",
	"attach_file",
	"bolt",
	"bug_report",
	"chat_bubble",
	"check",
	"check_circle",
	"chevron_right",
	"close",
	"cloud",
	"code",
	"content_copy",
	"contrast",
	"delete",
	"description",
	"desktop_windows",
	"developer_board",
	"devices",
	"difference",
	"dns",
	"draft",
	"edit",
	"expand_less",
	"expand_more",
	"filter_list",
	"find_in_page",
	"folder",
	"grid_on",
	"group",
	"host",
	"keep",
	"keep_off",
	"language",
	"laptop",
	"lightbulb",
	"logout",
	"mic",
	"more_horiz",
	"more_vert",
	"network_check",
	"notifications",
	"open_in_new",
	"palette",
	"photo",
	"play_arrow",
	"power_settings_new",
	"qr_code_scanner",
	"rate_review",
	"refresh",
	"search",
	"send",
	"sensors",
	"settings",
	"share",
	"smartphone",
	"speed",
	"stacks",
	"stop",
	"system_update",
	"tab",
	"terminal",
	"tune",
	"verified_user",
	"vertical_align_top",
	"visibility",
	"visibility_off",
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
