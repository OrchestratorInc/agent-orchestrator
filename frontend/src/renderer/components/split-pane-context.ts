import { createContext, useContext } from "react";

const NO_KEYS: ReadonlySet<string> = new Set();

export const SplitHiddenTabKeysContext = createContext<ReadonlySet<string>>(NO_KEYS);

export function useSplitHiddenTabKeys(): ReadonlySet<string> {
	return useContext(SplitHiddenTabKeysContext);
}

export const SplitCloseFocusedContext = createContext<(() => void) | undefined>(undefined);

export function useSplitCloseFocused(): (() => void) | undefined {
	return useContext(SplitCloseFocusedContext);
}
