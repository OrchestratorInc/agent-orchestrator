import type { PRCardMenuIcons } from "./pr-card-menu-icons.types";
import { symbols } from "./symbols";

export type { PRCardMenuIcons } from "./pr-card-menu-icons.types";

/**
 * Leading glyphs for a pull-request row's long-press menu. iOS names the SF
 * Symbol; ./pr-card-menu-icons.android hands Compose the bundled drawables,
 * since an Android menu item draws only an image source.
 */
export const prCardMenuIcons: PRCardMenuIcons = {
	openInGitHub: symbols.openExternal.ios,
	copyLink: symbols.copy.ios,
};
