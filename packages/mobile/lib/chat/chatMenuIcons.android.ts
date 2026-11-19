import type { MenuAction } from "@expo/ui/community/menu";
import { materialDrawable } from "../toolbarIcon.android";
import { chatMenuSymbols, type ChatMenuIcon } from "./chatMenuIcons.types";

export type { ChatMenuIcon } from "./chatMenuIcons.types";

/** A MenuView action's `image`: Compose's DropdownMenu draws only an image source. */
export function chatMenuIcon(name: ChatMenuIcon): MenuAction["image"] {
	return materialDrawable(chatMenuSymbols[name].android);
}
