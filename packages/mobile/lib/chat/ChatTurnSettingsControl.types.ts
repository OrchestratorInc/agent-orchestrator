import type { EffortChoice } from "../modelEffort";
import type { ChatConfigOption, ChatModel, ConversationSnapshot, TurnSettings } from "./types";

export type ChatTurnSettingsControlProps = {
	snapshot: ConversationSnapshot;
	models: ChatModel[];
	options: ChatConfigOption[];
	disabled?: boolean;
	onSettings(settings: TurnSettings): Promise<void>;
	onOption(id: string, value: { value: string } | { enabled: boolean }): Promise<ChatConfigOption[]>;
	/**
	 * The effort's speedometer: the levels lit to the chosen one, whether the
	 * composer's slider is open, and the tap that opens or closes it. None for
	 * a model that takes no effort.
	 */
	effort?: { choices: readonly EffortChoice[]; open: boolean; onToggle(): void };
};
