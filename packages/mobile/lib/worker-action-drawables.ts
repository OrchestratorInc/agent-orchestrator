import type { ImageSourcePropType } from "react-native";
import { workerActionSymbols, type WorkerActionDrawable, type WorkerActionId } from "./worker-action-model";

// Android's menu takes an image source, not a glyph name, so each action's
// Material Symbol is bundled as a drawable, written from the font by
// scripts/generate-material-drawables.mjs. A `require` of a missing file fails
// at bundle time rather than rendering blank, and the type keeps this map
// complete for every action. It requires .xml, so tests mock this module.
const drawables: Record<WorkerActionDrawable, ImageSourcePropType> = {
	arrow_forward: require("../assets/icons/arrow_forward.xml"),
	keep: require("../assets/icons/keep.xml"),
	keep_off: require("../assets/icons/keep_off.xml"),
	edit: require("../assets/icons/edit.xml"),
	refresh: require("../assets/icons/refresh.xml"),
	open_in_new: require("../assets/icons/open_in_new.xml"),
	archive: require("../assets/icons/archive.xml"),
};

/** The bundled Material Symbol for an action, for a menu that draws image sources. */
export function workerActionDrawable(id: WorkerActionId): ImageSourcePropType {
	return drawables[workerActionSymbols[id].android];
}
