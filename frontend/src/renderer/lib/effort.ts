import { useEffect, useRef } from "react";

const KNOWN_LEVEL_ORDER = ["none", "off", "minimal", "low", "medium", "high", "xhigh", "max"];

export function fallbackEffort(levels: string[], providerDefault?: string): string | undefined {
	const concrete = levels.filter((level) => level && level.toLowerCase() !== "default");
	if (concrete.length === 0) return undefined;
	if (providerDefault && concrete.includes(providerDefault)) return undefined;
	const ordered = concrete.every((level) => KNOWN_LEVEL_ORDER.includes(level))
		? [...concrete].sort((a, b) => KNOWN_LEVEL_ORDER.indexOf(a) - KNOWN_LEVEL_ORDER.indexOf(b))
		: concrete;
	return ordered[Math.floor((ordered.length - 1) / 2)];
}

export function useApplyEffortDefault(
	context: string,
	value: string | undefined,
	apply: (value: string) => void,
	{ disabled, applyOnMount }: { disabled?: boolean; applyOnMount?: boolean } = {},
) {
	const initialContext = useRef<string | undefined>(undefined);
	const contextChanged = useRef(false);
	const applied = useRef<string | undefined>(undefined);
	const applyRef = useRef(apply);
	applyRef.current = apply;
	useEffect(() => {
		if (disabled) return;
		if (initialContext.current === undefined) initialContext.current = context;
		else if (context !== initialContext.current) contextChanged.current = true;
		if (!value) return;
		if (!applyOnMount && !contextChanged.current) return;
		const key = JSON.stringify([context, value]);
		if (applied.current === key) return;
		applied.current = key;
		applyRef.current(value);
	}, [context, value, disabled, applyOnMount]);
}
