import { useCallback, useMemo, useRef } from "react";
import { withFallbackEffort, type EffortChoice } from "../modelEffort";
import { useEffortSliderSwitch } from "../useEffortSlider";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { followsAgentLabel, turnSettingsRows, turnSettingTopic, turnSettingWrite } from "./turnSettingsModel";

/**
 * The chat's effort, picked on the slider over the composer. The slider
 * walks the levels as the finger moves, but the conversation is told only
 * where the finger lifted (or, failing that, where the slider closed), so a
 * drag does not send a save per level.
 */
export function useChatEffort({ snapshot, models, options, disabled, onSettings, onOption }: ChatTurnSettingsControlProps) {
	const row = useMemo(
		() => turnSettingsRows(snapshot, models, options).find((item) => item.kind === "select" && turnSettingTopic(item) === "effort"),
		[snapshot, models, options],
	);
	// A provider's "Use agent effort" choice names no level, so it is no stop
	// on the slider. With it in effect, or no level reported, the fallback is
	// shown checked; nothing is saved until a level is picked.
	const choices = useMemo<EffortChoice[]>(() => withFallbackEffort(row?.choices
		.filter((choice) => !followsAgentLabel(choice.label))
		.map((choice) => ({ value: choice.value, label: choice.label, selected: choice.selected })) ?? []), [row]);
	const picked = useRef<string | undefined>(undefined);
	const latest = useRef<() => void>(() => {});
	latest.current = () => {
		const value = picked.current;
		picked.current = undefined;
		if (!row || value === undefined || row.choices.find((choice) => choice.selected)?.value === value) return;
		const write = turnSettingWrite(snapshot.settings, row, value);
		void (write.kind === "option" ? onOption(write.id, write.value) : onSettings(write.settings)).catch(() => {});
	};
	// Stable, so the slider's memoised host is left alone while the composer
	// re-renders under a streaming reply.
	const commit = useCallback(() => latest.current(), []);
	const choose = useCallback((value: string) => { picked.current = value; }, []);
	// Whether the slider is open lives in the switch, not in the composer's
	// state: the gauge and the slider host subscribe to it themselves.
	const slider = useEffortSliderSwitch();
	const close = useCallback(() => slider.setOpen(false), [slider]);
	return { choices, slider, busy: Boolean(disabled), close, choose, commit };
}
