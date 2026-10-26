import type { ImageSourcePropType } from "react-native";
import { materialDrawable } from "./toolbarIcon.android";
import { workerActionSymbols, type WorkerActionId } from "./worker-action-model";

/**
 * The bundled Material Symbol for an action, for a menu that draws image
 * sources rather than glyph names. It comes from the shared registry, which
 * requires .xml, so tests mock this module.
 */
export function workerActionDrawable(id: WorkerActionId): ImageSourcePropType {
	return materialDrawable(workerActionSymbols[id].android);
}
