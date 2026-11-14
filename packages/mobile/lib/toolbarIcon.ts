import { symbols } from "./symbols";
import type { ToolbarIcon, ToolbarSymbol } from "./toolbarIcon.types";

export type { MaterialDrawableName, ToolbarIcon, ToolbarSymbol, ToolbarSymbolName } from "./toolbarIcon.types";

/**
 * The `icon` for a native header item (`Stack.Toolbar.Button`, `.Menu`). iOS
 * takes the SF Symbol by name and draws it in the bar's tint; Android takes the
 * bundled Material Symbol drawable (./toolbarIcon.android).
 */
export function toolbarIcon(symbol: ToolbarSymbol): ToolbarIcon {
	return typeof symbol === "string" ? symbols[symbol].ios : symbol.ios;
}
