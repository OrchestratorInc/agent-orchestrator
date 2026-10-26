import type { ToolbarIcon } from "../toolbarIcon";
import { fileIconSymbols, type FileIconName } from "./fileIcons.types";

export { fileIconSymbols, type FileIconName } from "./fileIcons.types";

/**
 * The `icon` for a Files header or toolbar item, menu action or MenuView
 * action. iOS names the SF Symbol, which UIKit and SwiftUI tint themselves;
 * Android's take a bundled drawable (./fileIcons.android).
 */
export function fileIcon(name: FileIconName): ToolbarIcon {
	return fileIconSymbols[name].ios;
}
