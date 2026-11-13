import type { ConversationMenuAction, ConversationMenuGroup } from "./conversationMenuModel";

export type ConversationActionsPageProps = {
	groups: readonly ConversationMenuGroup[];
	onSelect(action: ConversationMenuAction): void;
};
