import { createContext } from "react";

/**
 * Whether a board lattice below may animate (see LatticeCometLoader): false
 * where it cannot be seen, as for a board row scrolled out of view or a board
 * whose screen is out of focus (boardRowVisibility).
 * True by default.
 */
export const LatticeActiveContext = createContext(true);
