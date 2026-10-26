import type { ImageSourcePropType } from "react-native";

// Compose's Icon draws an image source, not a glyph name, so the Material
// Symbol ships as a vector drawable (scripts/generate-material-drawables.mjs).
// Kept apart from the component so tests can stand in for the bundled file.
export const arrowDownwardDrawable: ImageSourcePropType = require("../../assets/icons/arrow_downward.xml");
