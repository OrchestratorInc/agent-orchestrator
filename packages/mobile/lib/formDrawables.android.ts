import type { ImageSourcePropType } from "react-native";

// Material Symbols the native forms and machine pages draw through Compose
// `Icon` or a header item, which take an image source rather than a glyph.
// Bundled from assets/icons by scripts/generate-material-drawables.mjs; kept in
// their own module so tests can stand in for the files.
export const formDrawables = {
	visibility: require("../assets/icons/visibility.xml") as ImageSourcePropType,
	visibilityOff: require("../assets/icons/visibility_off.xml") as ImageSourcePropType,
	edit: require("../assets/icons/edit.xml") as ImageSourcePropType,
	expandMore: require("../assets/icons/expand_more.xml") as ImageSourcePropType,
};
