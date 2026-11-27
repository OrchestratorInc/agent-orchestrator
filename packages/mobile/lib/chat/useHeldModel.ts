import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { partitionConfigOptions } from "../../../shared/chat/config-options";
import type { ChatConfigOption, ConversationSnapshot, TurnSettings } from "./types";

/** How long a picked model is shown ahead of the conversation's own before it is trusted again. */
export const MODEL_CONFIRM_MS = 8_000;

type Held = { optionId?: string; value: string };

/**
 * A model picked in the composer, shown at once rather than when the agent
 * reports it: the save is a round trip, and the pill and the list kept the old
 * model the whole way, which read as lag. Held until the conversation reports
 * it, the save fails, or MODEL_CONFIRM_MS passes; the effort slider holds a
 * level the same way.
 *
 * Wraps the composer's saves (`onSettings`, `onOption`) to notice a model
 * change, and returns the snapshot and options to draw with it in.
 */
export function useHeldModel(
	snapshot: ConversationSnapshot,
	options: ChatConfigOption[],
	onSettings: (settings: TurnSettings) => Promise<void>,
	onOption: (id: string, value: { value: string } | { enabled: boolean }) => Promise<ChatConfigOption[]>,
) {
	const [held, setHeld] = useState<Held | null>(null);
	const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
	useEffect(() => () => clearTimeout(timer.current), []);
	const hold = useCallback((next: Held) => {
		setHeld(next);
		clearTimeout(timer.current);
		timer.current = setTimeout(() => setHeld(null), MODEL_CONFIRM_MS);
	}, []);
	const drop = useCallback((was: Held) => setHeld((current) => (current === was ? null : current)), []);

	const modelOptionIds = useMemo(() => new Set(partitionConfigOptions(options).model.map((option) => option.id)), [options]);
	const latest = useRef({ snapshot, modelOptionIds, onSettings, onOption });
	latest.current = { snapshot, modelOptionIds, onSettings, onOption };

	const heldOnSettings = useCallback((settings: TurnSettings) => {
		const { snapshot: snap, onSettings: save } = latest.current;
		const next = settings.model && settings.model !== snap.settings.model ? { value: settings.model } : null;
		if (next) hold(next);
		return save(settings).catch((cause: unknown) => { if (next) drop(next); throw cause; });
	}, [hold, drop]);
	const heldOnOption = useCallback((id: string, value: { value: string } | { enabled: boolean }) => {
		const { modelOptionIds: ids, onOption: save } = latest.current;
		const next = ids.has(id) && "value" in value ? { optionId: id, value: value.value } : null;
		if (next) hold(next);
		return save(id, value).catch((cause: unknown) => { if (next) drop(next); throw cause; });
	}, [hold, drop]);

	// Reported: the conversation's own value stands again.
	const reported = held?.optionId
		? options.find((option) => option.id === held.optionId)?.currentValue
		: snapshot.settings.model;
	useEffect(() => { if (held && reported === held.value) setHeld(null); }, [held, reported]);

	const shownSnapshot = useMemo(
		() => (held && !held.optionId ? { ...snapshot, settings: { ...snapshot.settings, model: held.value } } : snapshot),
		[held, snapshot],
	);
	const shownOptions = useMemo(
		() => (held?.optionId ? options.map((option) => (option.id === held.optionId ? { ...option, currentValue: held.value } : option)) : options),
		[held, options],
	);
	return { snapshot: shownSnapshot, options: shownOptions, onSettings: heldOnSettings, onOption: heldOnOption };
}
