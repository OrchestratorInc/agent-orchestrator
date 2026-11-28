import { createContext, useContext } from "react";

/**
 * How far a page under a clear pill bar (see `pillScreenLayout`) has to hold
 * its content down to clear the bar: the bar's height there, 0 everywhere else.
 */
export const PillHeaderInsetContext = createContext(0);

export function usePillHeaderInset(): number {
	return useContext(PillHeaderInsetContext);
}
