import type { EffortGaugeToggles } from "../effort-gauge";
import type { EffortChoice } from "../modelEffort";
import type { EffortSliderSwitch } from "../useEffortSlider";
import type { PermissionChoice } from "../permission-picker";
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
	 * The effort's speedometer: the levels lit to the chosen one, and the
	 * switch of the composer's slider, which it opens and closes. None, or no
	 * choices, for a model that takes no effort, unless the slider carries a
	 * switch (`toggles`: Fast mode, Ultracode, shown on the gauge while on),
	 * which still needs the gauge.
	 */
	effort?: { choices: readonly EffortChoice[]; slider: EffortSliderSwitch; toggles?: EffortGaugeToggles };
	/**
	 * The permission mode's button: the modes, the one in force lit, and the
	 * switch of the picker it opens over the composer. None, or no choices,
	 * for a chat with no permission setting.
	 */
	permissions?: { choices: readonly PermissionChoice[]; slider: EffortSliderSwitch };
	/**
	 * The model's wheels over the keyboard (see useChatModelWheel): the
	 * capsule opens them in place of its menu. Absent, the capsule keeps its
	 * menu.
	 */
	modelWheel?: { slider: EffortSliderSwitch };
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
