import type { ImageSourcePropType } from "react-native";

// The spawn composer's Material Symbols, bundled as vector drawables because
// Compose `Icon` takes an image source rather than a glyph name. A `require` of
// a missing file fails at bundle time rather than drawing an empty icon, so this
// map and assets/icons must agree exactly.
export const spawnDrawables = {
	machine: require("../assets/icons/desktop_windows.xml"),
	standalone: require("../assets/icons/add_comment.xml"),
	dropDown: require("../assets/icons/arrow_drop_down.xml"),
	attach: require("../assets/icons/attach_file.xml"),
	photo: require("../assets/icons/photo.xml"),
	file: require("../assets/icons/draft.xml"),
	agent: require("../assets/icons/terminal.xml"),
} satisfies Record<string, ImageSourcePropType>;
