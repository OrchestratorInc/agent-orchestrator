import { useCallback, useMemo, useRef } from "react";
import type { PermissionChoice } from "../permission-picker";
import { useEffortSliderSwitch } from "../useEffortSlider";
import { approvalLook } from "./approvalCopy";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";
import type { ApprovalMode, ChatConfigOption, ChatModel, ConversationSnapshot } from "./types";

/**
 * The chat's permission mode for the picker over the composer: AO's approvals
 * or the provider's own permission choices, whichever the conversation uses,
 * each with the look of the mode it applies. Whether the picker is open lives
 * in its switch, not in the composer's state, as the effort slider's does.
 */
/**
 * The permission row a conversation's settings make, and its choices as the
 * picker lists them: by the agent's own names where it reports its modes
 * (Claude's "Manual", "Accept Edits"), each with the look of the mode it
 * applies. New task lists its modes through this too, from the settings the
 * agent last reported in a chat (chatSettingsTemplate), so the two pickers
 * name a mode alike.
 */
export function permissionChoices(snapshot: Pick<ConversationSnapshot, "harness" | "settings" | "capabilities" | "controller" | "modelReroute">, models: ChatModel[], options: ChatConfigOption[]): { row: TurnSettingRow | undefined; choices: (PermissionChoice & { permissionMode?: ApprovalMode })[] } {
	const row = turnSettingsRows(snapshot as ConversationSnapshot, models, options).find((item) => item.kind === "select" && turnSettingTopic(item) === "permissions");
	const choices = row?.choices.map((choice) => ({
		value: choice.value,
		label: choice.label,
		description: choice.description,
		selected: choice.selected,
		look: choice.permissionMode ? approvalLook(snapshot.harness, choice.permissionMode) : undefined,
		permissionMode: choice.permissionMode,
	})) ?? [];
	return { row, choices };
}

export function useChatPermissions({ snapshot, models, options, disabled, onSettings, onOption }: ChatTurnSettingsControlProps) {
	const { row, choices } = useMemo(() => permissionChoices(snapshot, models, options), [snapshot, models, options]);
	const latest = useRef<(value: string) => void>(() => {});
	latest.current = (value) => {
		if (!row || row.choices.find((choice) => choice.value === value)?.selected) return;
		const write = turnSettingWrite(snapshot.settings, row, value);
		void (write.kind === "option" ? onOption(write.id, write.value) : onSettings(write.settings)).catch(() => {});
	};
	// Stable, so the picker's memoised host is left alone while the composer
	// re-renders under a streaming reply.
	const choose = useCallback((value: string) => latest.current(value), []);
	const slider = useEffortSliderSwitch();
	const close = useCallback(() => slider.setOpen(false), [slider]);
	return { choices, slider, busy: Boolean(disabled), close, choose };
}
