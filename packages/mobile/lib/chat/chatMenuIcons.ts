import type { MenuAction } from "@expo/ui/community/menu";
import { chatMenuSymbols, type ChatMenuIcon } from "./chatMenuIcons.types";

export type { ChatMenuIcon } from "./chatMenuIcons.types";

/**
 * A MenuView action's `image`. iOS takes the SF Symbol by name; Android draws
 * only an image source, so ./chatMenuIcons.android hands it the bundled Material
 * Symbol drawable instead.
 */
export function chatMenuIcon(name: ChatMenuIcon): MenuAction["image"] {
	return chatMenuSymbols[name].ios;
}
