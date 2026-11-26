import { createContext, useContext } from "react";

const NO_KEYS: ReadonlySet<string> = new Set();

/**
 * Tab keys shown together in a split group. The tab strips (terminal and
 * chat) leave them out; each group draws one tab of its own instead.
 */
export const SplitHiddenTabKeysContext = createContext<ReadonlySet<string>>(NO_KEYS);

export function useSplitHiddenTabKeys(): ReadonlySet<string> {
	return useContext(SplitHiddenTabKeysContext);
}

/**
 * Closes the focused pane of the split on screen; undefined when no split is
 * shown. The tab strips route ⌘W here before their own tab.
 */
export const SplitCloseFocusedContext = createContext<(() => void) | undefined>(undefined);

export function useSplitCloseFocused(): (() => void) | undefined {
	return useContext(SplitCloseFocusedContext);
}
