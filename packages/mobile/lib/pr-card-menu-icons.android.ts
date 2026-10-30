import type { PRCardMenuIcons } from "./pr-card-menu-icons.types";
import { materialDrawable } from "./toolbarIcon.android";

export type { PRCardMenuIcons } from "./pr-card-menu-icons.types";

// A Compose menu item draws only an image source, so each glyph is the shared
// registry's drawable for the Material Symbol of the registry entry iOS names.
export const prCardMenuIcons: PRCardMenuIcons = {
	openInGitHub: materialDrawable("open_in_new"),
	copyLink: materialDrawable("content_copy"),
};
