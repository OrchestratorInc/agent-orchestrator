import type { MenuAction } from "@expo/ui/community/menu";
import type { ImageSourcePropType } from "react-native";
import type { ChatMenuIcon } from "./chatMenuIcons";

export type { ChatMenuIcon } from "./chatMenuIcons";

// Bundled, not resolved by name: a `require` of a missing file fails at bundle
// time rather than drawing an empty row, and the type keeps this map complete.
// Each file is the Material Symbol of the same name, written by
// scripts/generate-material-drawables.mjs.
const drawables: Record<ChatMenuIcon, ImageSourcePropType> = {
	edit: require("../../assets/icons/edit.xml"),
	copy: require("../../assets/icons/content_copy.xml"),
	photo: require("../../assets/icons/photo.xml"),
	file: require("../../assets/icons/draft.xml"),
	model: require("../../assets/icons/stacks.xml"),
	effort: require("../../assets/icons/speed.xml"),
	fast: require("../../assets/icons/bolt.xml"),
	permissions: require("../../assets/icons/verified_user.xml"),
	setting: require("../../assets/icons/tune.xml"),
};

/** A MenuView action's `image`: Compose's DropdownMenu draws only an image source. */
export function chatMenuIcon(name: ChatMenuIcon): MenuAction["image"] {
	return drawables[name];
}
