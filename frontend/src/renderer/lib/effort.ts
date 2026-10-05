import { useEffect, useRef } from "react";

const KNOWN_LEVEL_ORDER = ["none", "off", "minimal", "low", "medium", "high", "xhigh", "max"];

/**
 * The concrete level AO selects when a model reports effort levels but no
 * usable default for them. It is the middle level, so the picker never shows an
 * unselected state and the run uses exactly the level the picker shows.
 *
 * Returns undefined when the provider's default is one of the levels, so the
 * provider stays the source of truth whenever it names one.
 */
export function fallbackEffort(levels: string[], providerDefault?: string): string | undefined {
	const concrete = levels.filter((level) => level && level.toLowerCase() !== "default");
	if (concrete.length === 0) return undefined;
	if (providerDefault && concrete.includes(providerDefault)) return undefined;
	const ordered = concrete.every((level) => KNOWN_LEVEL_ORDER.includes(level))
		? [...concrete].sort((a, b) => KNOWN_LEVEL_ORDER.indexOf(a) - KNOWN_LEVEL_ORDER.indexOf(b))
		: concrete;
	return ordered[Math.floor((ordered.length - 1) / 2)];
}

/**
 * Applies AO's fallback effort once per context (for example per model), so a
 * level the picker shows as selected is also set on the agent. It never fires
 * again for the same context, which keeps a level the user later changes or
 * clears from being overwritten.
 */
export function useApplyEffortDefault(
	context: string,
	value: string | undefined,
	apply: (value: string) => void,
	disabled?: boolean,
) {
	const applied = useRef<string | undefined>(undefined);
	const applyRef = useRef(apply);
	applyRef.current = apply;
	useEffect(() => {
		if (disabled || !value) return;
		const key = JSON.stringify([context, value]);
		if (applied.current === key) return;
		applied.current = key;
		applyRef.current(value);
	}, [context, value, disabled]);
}
