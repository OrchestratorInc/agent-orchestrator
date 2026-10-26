import type { PRCardMenuIcons } from "./pr-card-menu-icons.types";
import { materialDrawable } from "./toolbarIcon.android";

export type { PRCardMenuIcons } from "./pr-card-menu-icons.types";

// Bundled rather than resolved by name: a Compose menu item draws only an image
// source, and a `require` of a missing file fails at bundle time.
export const prCardMenuIcons: PRCardMenuIcons = {
	openInGitHub: materialDrawable("open_in_new"),
	copyLink: require("../assets/icons/content_copy.xml"),
};
