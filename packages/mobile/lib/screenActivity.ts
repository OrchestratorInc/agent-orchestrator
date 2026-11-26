import { createContext, useContext } from "react";

/**
 * Whether the screen a view sits in can be seen: the top of its stack, or under
 * nothing but a sheet. Decoration that redraws every frame on the UI thread (a
 * working lattice) holds still while it cannot.
 *
 * A board tab stays live under a page pushed over it (TabStack), so its rows
 * kept their lattices running beneath an open chat: eight working sessions were
 * ninety-six dots animating every frame, and every React commit in the chat
 * applied all of them again, while the chat streamed its reply.
 *
 * Outside any screen (a test, a preview) a view counts as seen.
 */
export const ScreenActiveContext = createContext(true);

export function useScreenActive(): boolean {
	return useContext(ScreenActiveContext);
}
