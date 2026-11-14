import { useState, useSyncExternalStore } from "react";

/**
 * A yes/no one component sets and another reads, without re-rendering the
 * component that holds it. The chat screen held the composer's open state as
 * its own: each open and close re-rendered the whole screen, the conversation
 * with it, while the keyboard was moving.
 */
export type Flag = {
	get(): boolean;
	set(value: boolean): void;
	subscribe(listener: () => void): () => void;
};

export function createFlag(initial = false): Flag {
	let value = initial;
	const listeners = new Set<() => void>();
	return {
		get: () => value,
		set: (next) => {
			if (next === value) return;
			value = next;
			for (const listener of [...listeners]) listener();
		},
		subscribe: (listener) => {
			listeners.add(listener);
			return () => { listeners.delete(listener); };
		},
	};
}

/** One flag for the life of the component. */
export function useFlag(initial = false): Flag {
	const [flag] = useState(() => createFlag(initial));
	return flag;
}

const never = () => () => {};
const no = () => false;

/** The flag's value, re-rendering only the component that reads it. */
export function useFlagValue(flag: Flag | undefined): boolean {
	return useSyncExternalStore(flag?.subscribe ?? never, flag?.get ?? no, flag?.get ?? no);
}
