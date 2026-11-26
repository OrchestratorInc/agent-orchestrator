import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { FastToggle } from "../effort-slider";
import { withFallbackEffort, type EffortChoice } from "../modelEffort";
import { useEffortSliderSwitch } from "../useEffortSlider";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { followsAgentLabel, turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";
import { offersUltracode, ULTRACODE, ULTRACODE_LEVEL } from "./ultracode";

/** How long a saved level is shown ahead of the conversation's own before the conversation's is trusted again. */
export const EFFORT_CONFIRM_MS = 8_000;

/**
 * The chat's effort, picked on the slider over the composer. The slider
 * walks the levels as the finger moves, but the conversation is told only
 * where the finger lifted (or, failing that, where the slider closed), so a
 * drag does not send a save per level.
 *
 * The gauge and the slider show one level, the switch's pick (see
 * EffortSliderSwitch): the level under the finger while it drags, then the
 * one saved, held until the conversation reports it. The pick is dropped,
 * and both show the conversation's level again, when the conversation
 * reports it, when the save fails, when the model no longer offers it, or
 * when the conversation has not caught up within EFFORT_CONFIRM_MS. Only the
 * newest save can drop it: an older one finishing late changes nothing.
 */
export function useChatEffort({ snapshot, models, options, disabled, onSettings, onOption }: ChatTurnSettingsControlProps) {
	const rows = useMemo(() => turnSettingsRows(snapshot, models, options), [snapshot, models, options]);
	const row = rows.find((item) => item.kind === "select" && turnSettingTopic(item) === "effort");
	const fast = useFastToggle(rows, { snapshot, onSettings, onOption });
	// A provider's "Use agent effort" choice names no level, so it is no stop
	// on the slider. With it in effect, or no level reported, the fallback is
	// shown checked; nothing is saved until a level is picked.
	const saved = row?.choices.find((choice) => choice.selected)?.value;
	// Claude Code's Ultracode, the level above Extra High, as desktop offers it:
	// saved as Extra High, and asked for in each message while it is on.
	const ultracodeOffered = offersUltracode(snapshot.harness, row?.choices.map((choice) => choice.value) ?? []);
	const [ultracodeWanted, setUltracode] = useState(false);
	const ultracode = ultracodeOffered && ultracodeWanted && saved === ULTRACODE_LEVEL;
	useEffect(() => { if (!ultracodeOffered) setUltracode(false); }, [ultracodeOffered]);
	const choices = useMemo<EffortChoice[]>(() => {
		const levels = withFallbackEffort(row?.choices
			.filter((choice) => !followsAgentLabel(choice.label))
			.map((choice) => ({ value: choice.value, label: choice.label, selected: choice.selected })) ?? []);
		if (!ultracodeOffered) return levels;
		// With Ultracode on, it is the level shown, not the Extra High it saves.
		return [
			...levels.map((choice) => (ultracode && choice.value === ULTRACODE_LEVEL ? { ...choice, selected: false } : choice)),
			{ value: ULTRACODE, label: "Ultracode", selected: ultracode },
		];
	}, [row, ultracodeOffered, ultracode]);
	// Whether the slider is open, and the level being picked, live in the
	// switch, not in the composer's state: the gauge and the slider host
	// subscribe to it themselves.
	const slider = useEffortSliderSwitch();
	// The level the finger is on and has not yet been saved.
	const picked = useRef<string | undefined>(undefined);
	// Each save's number; only the newest may drop the pick.
	const saves = useRef(0);
	const confirmTimer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	const stopConfirmTimer = () => {
		if (confirmTimer.current !== undefined) clearTimeout(confirmTimer.current);
		confirmTimer.current = undefined;
	};
	const latest = useRef<() => void>(() => {});
	// Published once the render commits: a render React drops (a model
	// change mid-drag, say) must not swap in a handler for a row never shown.
	useLayoutEffect(() => {
		latest.current = () => {
			const pickedValue = picked.current;
			picked.current = undefined;
			if (pickedValue === undefined) return;
			stopConfirmTimer();
			// Ultracode is Extra High saved, and on for the messages that follow.
			setUltracode(pickedValue === ULTRACODE);
			const value = pickedValue === ULTRACODE ? ULTRACODE_LEVEL : pickedValue;
			const save = ++saves.current;
			// Back on the level already saved: nothing to send, nothing to hold.
			if (!row || saved === value) {
				slider.setPick(undefined);
				return;
			}
			const write = turnSettingWrite(snapshot.settings, row, value);
			const drop = () => {
				if (saves.current !== save || picked.current !== undefined) return;
				stopConfirmTimer();
				slider.setPick(undefined);
				if (pickedValue === ULTRACODE) setUltracode(false);
			};
			// Held until the conversation shows it (the effect below), or dropped
			// if it never does.
			confirmTimer.current = setTimeout(drop, EFFORT_CONFIRM_MS);
			void (write.kind === "option" ? onOption(write.id, write.value) : onSettings(write.settings)).catch(drop);
		};
	});
	// Stable, so the slider's memoised host is left alone while the composer
	// re-renders under a streaming reply.
	const commit = useCallback(() => latest.current(), []);
	const choose = useCallback((value: string) => {
		picked.current = value;
		slider.setPick(value);
	}, [slider]);
	// The conversation now reports the level picked, or no longer offers it:
	// show its own again. Not while a finger is still choosing.
	const offered = useMemo(() => new Set(choices.map((choice) => choice.value)), [choices]);
	useEffect(() => {
		const pick = slider.pick();
		if (pick === undefined || picked.current !== undefined) return;
		if ((pick === ULTRACODE ? ULTRACODE_LEVEL : pick) === saved || !offered.has(pick)) {
			stopConfirmTimer();
			slider.setPick(undefined);
		}
	}, [saved, offered, slider]);
	useEffect(() => stopConfirmTimer, []);
	const close = useCallback(() => slider.setOpen(false), [slider]);
	return { choices, slider, busy: Boolean(disabled), close, choose, commit, ultracode, fast };
}

/**
 * Fast mode, where the model has it as an on/off: flipped from the bolt over
 * the effort slider. The flip shows at once and is held until the
 * conversation reports it, dropped if the save fails or the conversation has
 * not caught up within EFFORT_CONFIRM_MS.
 */
function useFastToggle(rows: TurnSettingRow[], { snapshot, onSettings, onOption }: Pick<ChatTurnSettingsControlProps, "snapshot" | "onSettings" | "onOption">): FastToggle | undefined {
	const row = rows.find((item) => item.kind === "boolean" && turnSettingTopic(item) === "fast");
	const saved = Boolean(row?.enabled);
	const [flipped, setFlipped] = useState<boolean | undefined>(undefined);
	const unconfirmed = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => { setFlipped(undefined); }, [saved]);
	useEffect(() => () => clearTimeout(unconfirmed.current), []);
	const latest = useRef({ row, snapshot, onSettings, onOption, on: flipped ?? saved });
	latest.current = { row, snapshot, onSettings, onOption, on: flipped ?? saved };
	// Stable, so the slider's memoised host is left alone between flips.
	const toggle = useCallback(() => {
		const { row: current, snapshot: snap, onSettings: save, onOption: option, on } = latest.current;
		if (!current) return;
		const next = !on;
		setFlipped(next);
		// Not reported within the effort's wait: show the conversation's own again.
		if (unconfirmed.current !== undefined) clearTimeout(unconfirmed.current);
		unconfirmed.current = setTimeout(() => setFlipped(undefined), EFFORT_CONFIRM_MS);
		const write = turnSettingWrite(snap.settings, current, next);
		void (write.kind === "option" ? option(write.id, write.value) : save(write.settings)).catch(() => setFlipped(undefined));
	}, []);
	const on = flipped ?? saved;
	const offered = Boolean(row);
	return useMemo(() => (offered ? { on, toggle } : undefined), [offered, on, toggle]);
}
