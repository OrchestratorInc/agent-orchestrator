import type { EffortChoice } from "../modelEffort";
import type { ChatConfigOption, ChatModel, ConversationSnapshot, TurnSettings } from "./types";
import type { AgentSwitchMenu } from "./useAgentSwitchMenu";
import type { PendingAgentSwitch } from "../agentSwitch";

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
	/**
	 * The capsule's "Other agents", as the new task's menu lists them: each
	 * agent opens onto its models. Choosing one only records it (`pending`);
	 * the switch runs with the next message, as on desktop. Choosing one of the
	 * running agent's models again drops the pick. Absent when there is no
	 * other agent to hand the chat to.
	 */
	otherAgents?: AgentSwitchMenu & {
		pending?: PendingAgentSwitch;
		/** The agent is working; a switch waits until it is idle. */
		disabled?: boolean;
		onPick(pick: PendingAgentSwitch | undefined): void;
	};
};
