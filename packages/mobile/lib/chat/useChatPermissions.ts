import { useCallback, useMemo, useRef } from "react";
import type { PermissionChoice } from "../permission-picker";
import { useEffortSliderSwitch } from "../useEffortSlider";
import { approvalLook } from "./approvalCopy";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { turnSettingsRows, turnSettingTopic, turnSettingWrite } from "./turnSettingsModel";

/**
 * The chat's permission mode for the picker over the composer: AO's approvals
 * or the provider's own permission choices, whichever the conversation uses,
 * each with the look of the mode it applies. Whether the picker is open lives
 * in its switch, not in the composer's state, as the effort slider's does.
 */
export function useChatPermissions({ snapshot, models, options, disabled, onSettings, onOption }: ChatTurnSettingsControlProps) {
	const row = useMemo(
		() => turnSettingsRows(snapshot, models, options).find((item) => item.kind === "select" && turnSettingTopic(item) === "permissions"),
		[snapshot, models, options],
	);
	const choices = useMemo<PermissionChoice[]>(() => row?.choices.map((choice) => ({
		value: choice.value,
		label: choice.label,
		description: choice.description,
		selected: choice.selected,
		look: choice.permissionMode ? approvalLook(snapshot.harness, choice.permissionMode) : undefined,
	})) ?? [], [row, snapshot.harness]);
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
