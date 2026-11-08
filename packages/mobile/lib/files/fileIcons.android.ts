import type { ImageSourcePropType } from "react-native";
import { materialDrawable, type ToolbarIcon } from "../toolbarIcon.android";
import { fileIconSymbols, type FileIconName } from "./fileIcons.types";

export { fileIconSymbols, type FileIconName } from "./fileIcons.types";

// Android's header items, Compose menus and MenuView actions draw only an
// image source, so each glyph is the shared registry's drawable for the
// Material Symbol named in ./fileIcons.types.
export function fileIcon(name: FileIconName): ToolbarIcon {
	return fileDrawable(name);
}

/** The bundled drawable itself, for Compose `Icon`. Import it from this file, in .android files only. */
export function fileDrawable(name: FileIconName): ImageSourcePropType {
	return materialDrawable(fileIconSymbols[name].android);
}
