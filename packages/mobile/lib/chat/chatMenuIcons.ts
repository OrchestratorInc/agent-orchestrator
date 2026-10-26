import type { MenuAction } from "@expo/ui/community/menu";
import { symbols, type IOSSymbol } from "../symbols";

/** The glyphs the chat's native menus draw beside their actions, by job. */
export type ChatMenuIcon = "edit" | "copy" | "photo" | "file" | "model" | "effort" | "fast" | "permissions" | "setting";

const sfSymbols: Record<ChatMenuIcon, IOSSymbol> = {
	edit: symbols.rename.ios,
	copy: symbols.copy.ios,
	photo: symbols.photo.ios,
	file: symbols.file.ios,
	model: "square.stack.3d.up",
	effort: "gauge.medium",
	fast: "bolt",
	permissions: "checkmark.shield",
	setting: "slider.horizontal.3",
};

/**
 * A MenuView action's `image`. iOS takes the SF Symbol by name; Android draws
 * only an image source, so ./chatMenuIcons.android hands it the bundled Material
 * Symbol drawable instead.
 */
export function chatMenuIcon(name: ChatMenuIcon): MenuAction["image"] {
	return sfSymbols[name];
}
