import { createContext } from "react";

/**
 * Whether a lattice below may animate (see LatticeLoader): false where it
 * cannot be seen, as for a board row scrolled out of view (boardRowVisibility).
 * True by default.
 */
export const LatticeActiveContext = createContext(true);
