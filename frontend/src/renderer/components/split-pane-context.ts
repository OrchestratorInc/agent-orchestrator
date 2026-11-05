import { createContext, useContext } from "react";

const NO_KEYS: ReadonlySet<string> = new Set();

/**
 * Tab keys that moved to the split's side pane. The main tab strips (terminal
 * and chat) leave them out; the side pane draws its own strip for them.
 */
export const SplitHiddenTabKeysContext = createContext<ReadonlySet<string>>(NO_KEYS);

export function useSplitHiddenTabKeys(): ReadonlySet<string> {
	return useContext(SplitHiddenTabKeysContext);
}
