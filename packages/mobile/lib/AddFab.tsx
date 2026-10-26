import type { AddFabProps } from "./AddFab.types";

export type { AddFabProps } from "./AddFab.types";

/**
 * iOS has no floating action button: a screen's create action is a "+" in its
 * header, so there is nothing to float here. Android's is ./AddFab.android.
 */
export function AddFab(_props: AddFabProps): null {
	return null;
}

/** How much of a list's bottom the button covers; nothing, with none drawn. */
export const addFabClearance = 0;
