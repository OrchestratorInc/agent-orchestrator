import type { ImageSourcePropType } from "react-native";
import type { ToolbarIcon } from "../toolbarIcon";
import type { FileIconName } from "./fileIcons.types";

export { fileIconSymbols, type FileIconName } from "./fileIcons.types";

// Bundled, not resolved by name: Android's header items, Compose menus and
// MenuView actions draw only an image source, and a `require` of a missing
// file fails at bundle time instead of drawing an empty button. Each file is
// the Material Symbol named in ./fileIcons.types.
const drawables: Record<FileIconName, ImageSourcePropType> = {
	previous: require("../../assets/icons/expand_less.xml"),
	next: require("../../assets/icons/expand_more.xml"),
	more: require("../../assets/icons/more_vert.xml"),
	wrapLines: require("../../assets/icons/wrap_text.xml"),
	copy: require("../../assets/icons/content_copy.xml"),
	share: require("../../assets/icons/share.xml"),
	openDiff: require("../../assets/icons/difference.xml"),
	openFile: require("../../assets/icons/description.xml"),
	preview: require("../../assets/icons/visibility.xml"),
	source: require("../../assets/icons/code.xml"),
	review: require("../../assets/icons/rate_review.xml"),
	delete: require("../../assets/icons/delete.xml"),
};

export function fileIcon(name: FileIconName): ToolbarIcon {
	return drawables[name];
}
