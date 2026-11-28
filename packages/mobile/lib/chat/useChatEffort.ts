import AsyncStorage from "@react-native-async-storage/async-storage";
import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { FastToggle } from "../effort-slider";
import { withFallbackEffort, type EffortChoice } from "../modelEffort";
import { useEffortSliderSwitch } from "../useEffortSlider";
import type { ChatTurnSettingsControlProps } from "./ChatTurnSettingsControl.types";
import { followsAgentLabel, turnSettingsRows, turnSettingTopic, turnSettingWrite, type TurnSettingRow } from "./turnSettingsModel";
import type { TurnSettings } from "./types";
import { offersUltracode, ULTRACODE_HARNESS } from "./ultracode";

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
export function useChatEffort({ snapshot, models, options, disabled, onSettings, onOption, ultracodeKey, cloud = false }: ChatTurnSettingsControlProps & {
	/** Where this conversation's Ultracode is kept on the phone, so it lasts past this screen; none keeps it in memory. */
	ultracodeKey?: string | null;
	/** An Orchestrator Cloud chat, which has no Ultracode, as on desktop. */
	cloud?: boolean;
}) {
	const rows = useMemo(() => turnSettingsRows(snapshot, models, options), [snapshot, models, options]);
	const row = rows.find((item) => item.kind === "select" && turnSettingTopic(item) === "effort");
	// The level last saved and not yet reported: a Fast flip's save replaces
	// the whole set too, so it carries this.
	const held = useRef<string | undefined>(undefined);
	const effortRow = useRef(row);
	effortRow.current = row;
	const withHeldEffort = useCallback((base: TurnSettings): TurnSettings => {
		const value = held.current;
		const current = effortRow.current;
		if (value === undefined || !current) return base;
		const write = turnSettingWrite(base, current, value);
		return write.kind === "settings" ? write.settings : base;
	}, []);
	const fast = useFastToggle(rows, { snapshot, onSettings, onOption }, withHeldEffort);
	// Claude Code's Ultracode, a switch over the slider apart from the level,
	// as desktop has it: on at any level, on every model, it only asks for
	// Ultracode at the start of each message. The daemon has no field for it,
	// so it is kept per conversation on the phone.
	const ultracodeOffered = offersUltracode(snapshot.harness, !cloud);
	const [ultracodeWanted, setUltracode] = usePhoneFlag(ultracodeKey);
	// Seen running another agent, as the chat opens or once a switch away is
	// reported, the conversation's Ultracode is off, as desktop's is after a
	// switch away: a switch back to Claude Code must not bring back a flag
	// nobody turned on again. A switch made here sets it to the switch's own
	// choice as it lands (`keepUltracode`), which can be before the chat
	// reports its new agent, so it is turned off only when an agent is first
	// seen, never again for the one already seen.
	const agentSeen = useRef<string | undefined>(undefined);
	useEffect(() => {
		const harness = snapshot.harness;
		const seen = `${ultracodeKey ?? ""}\n${harness ?? ""}`;
		if (!harness || agentSeen.current === seen) return;
		agentSeen.current = seen;
		if (harness !== ULTRACODE_HARNESS) setUltracode(false);
	}, [snapshot.harness, ultracodeKey, setUltracode]);
	const ultracodeNow = useRef({ offered: ultracodeOffered, wanted: ultracodeWanted });
	ultracodeNow.current = { offered: ultracodeOffered, wanted: ultracodeWanted };
	// Whether the next message asks for Ultracode. Read at send time, and
	// apart from the effort row: a message sent before the conversation
	// reports its levels still asks for it.
	const sendsUltracode = useCallback(() => ultracodeNow.current.offered && ultracodeNow.current.wanted, []);
	// Stable, so the slider's memoised host is left alone between flips.
	const toggleUltracode = useCallback(() => setUltracode(!ultracodeNow.current.wanted), [setUltracode]);
	const ultracode = useMemo<FastToggle | undefined>(
		() => (ultracodeOffered ? { on: ultracodeWanted, toggle: toggleUltracode } : undefined),
		[ultracodeOffered, ultracodeWanted, toggleUltracode],
	);
	// A provider's "Use agent effort" choice names no level, so it is no stop
	// on the slider. With it in effect, or no level reported, the fallback is
	// shown checked; nothing is saved until a level is picked.
	const saved = row?.choices.find((choice) => choice.selected)?.value;
	const choices = useMemo<EffortChoice[]>(() => withFallbackEffort(row?.choices
		.filter((choice) => !followsAgentLabel(choice.label))
		.map((choice) => ({ value: choice.value, label: choice.label, selected: choice.selected })) ?? []), [row]);
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
			const value = picked.current;
			picked.current = undefined;
			if (value === undefined) return;
			// The level already on its way: its save and hold stand.
			if (row && held.current !== undefined && value === held.current) return;
			stopConfirmTimer();
			const save = ++saves.current;
			// Back on the level already saved, with none on its way: nothing to
			// send, nothing to hold. Back on it with another level on its way,
			// the save goes, or that level would land over it.
			if (!row || value === (held.current ?? saved)) {
				held.current = undefined;
				slider.setPick(undefined);
				return;
			}
			// Built on the Fast value last picked, not only the one reported:
			// the save replaces the whole set, and a Fast flip not yet reported
			// would otherwise be cleared by it.
			const write = turnSettingWrite(fast?.settings(snapshot.settings) ?? snapshot.settings, row, value);
			held.current = value;
			const drop = () => {
				if (saves.current !== save) return;
				held.current = undefined;
				if (picked.current !== undefined) return;
				stopConfirmTimer();
				slider.setPick(undefined);
			};
			// Held until the conversation shows it (the effect below), or dropped
			// if it never does, or its save fails.
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
		if (held.current !== undefined && (held.current === saved || !row?.choices.some((choice) => choice.value === held.current))) held.current = undefined;
		const pick = slider.pick();
		if (pick === undefined || picked.current !== undefined) return;
		if (pick === saved || !offered.has(pick)) {
			stopConfirmTimer();
			slider.setPick(undefined);
		}
	}, [saved, offered, slider, row]);
	useEffect(() => stopConfirmTimer, []);
	const close = useCallback(() => slider.setOpen(false), [slider]);
	return { choices, slider, busy: Boolean(disabled), close, choose, commit, ultracode, sendsUltracode, keepUltracode: setUltracode, fast };
}

/**
 * Fast mode, where the model has it as an on/off: flipped from the bolt over
 * the effort slider. The flip shows at once and is held until the
 * conversation reports it, dropped if its save fails or the conversation has
 * not caught up within EFFORT_CONFIRM_MS. Only the newest flip's report or
 * failure drops it: a report of an older flip, or an older save failing,
 * leaves the newest showing.
 */
function useFastToggle(rows: TurnSettingRow[], { snapshot, onSettings, onOption }: Pick<ChatTurnSettingsControlProps, "snapshot" | "onSettings" | "onOption">, withHeldEffort: (base: TurnSettings) => TurnSettings): (FastToggle & { settings: (base: TurnSettings) => TurnSettings }) | undefined {
	const row = rows.find((item) => item.kind === "boolean" && turnSettingTopic(item) === "fast");
	const saved = Boolean(row?.enabled);
	const [flipped, setFlipped] = useState<boolean | undefined>(undefined);
	const unconfirmed = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	// Each flip's number; only the newest may drop the flip on failure.
	const flips = useRef(0);
	useEffect(() => { setFlipped((current) => (current === saved ? undefined : current)); }, [saved]);
	useEffect(() => () => clearTimeout(unconfirmed.current), []);
	const latest = useRef({ row, snapshot, onSettings, onOption, on: flipped ?? saved, flipped });
	latest.current = { row, snapshot, onSettings, onOption, on: flipped ?? saved, flipped };
	// Stable, so the slider's memoised host is left alone between flips.
	const toggle = useCallback(() => {
		const { row: current, snapshot: snap, onSettings: save, onOption: option, on } = latest.current;
		if (!current) return;
		const next = !on;
		const flip = ++flips.current;
		setFlipped(next);
		// Not reported within the effort's wait: show the conversation's own again.
		if (unconfirmed.current !== undefined) clearTimeout(unconfirmed.current);
		unconfirmed.current = setTimeout(() => setFlipped(undefined), EFFORT_CONFIRM_MS);
		// Built on the effort last saved, not only the one reported: an effort
		// pick not yet reported would otherwise be reverted by this save.
		const write = turnSettingWrite(withHeldEffort(snap.settings), current, next);
		void (write.kind === "option" ? option(write.id, write.value) : save(write.settings)).catch(() => {
			if (flips.current === flip) setFlipped(undefined);
		});
	}, [withHeldEffort]);
	// Another save's settings with the flip not yet reported in them, where Fast
	// is one of AO's own settings rather than a provider option of its own.
	const settings = useCallback((base: TurnSettings): TurnSettings => {
		const { row: current, flipped: pending } = latest.current;
		if (!current || pending === undefined) return base;
		const write = turnSettingWrite(base, current, pending);
		return write.kind === "settings" ? write.settings : base;
	}, []);
	const on = flipped ?? saved;
	const offered = Boolean(row);
	return useMemo(() => (offered ? { on, toggle, settings } : undefined), [offered, on, toggle, settings]);
}

/**
 * An on/off kept on the phone under `key`, read back when the key is first
 * seen. A change made before the read lands wins over it.
 */
function usePhoneFlag(key: string | null | undefined): [boolean, (on: boolean) => void] {
	const [on, setOn] = useState(false);
	const keyRef = useRef(key);
	keyRef.current = key;
	const changed = useRef(false);
	useEffect(() => {
		changed.current = false;
		setOn(false);
		if (!key) return;
		let live = true;
		void AsyncStorage.getItem(key)
			.then((value) => { if (live && !changed.current) setOn(value === "1"); })
			.catch(() => {});
		return () => { live = false; };
	}, [key]);
	const set = useCallback((next: boolean) => {
		changed.current = true;
		setOn(next);
		const at = keyRef.current;
		if (at) void (next ? AsyncStorage.setItem(at, "1") : AsyncStorage.removeItem(at)).catch(() => {});
	}, []);
	return [on, set];
}
