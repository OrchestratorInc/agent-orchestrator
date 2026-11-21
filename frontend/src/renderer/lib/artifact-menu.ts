import type { MutableRefObject, ReactNode } from "react";

/**
 * One action a file view offers in the artifact viewer's file menu instead of
 * as a header button. A `mode` action is one of the view's display modes:
 * those share a single-choice group, with the shown one checked.
 */
export type ArtifactMenuAction = {
	id: string;
	label: string;
	icon: ReactNode;
	onSelect: () => void;
	disabled?: boolean;
	/** Set on a display-mode choice: whether it is the mode shown. */
	mode?: { checked: boolean };
};

/**
 * Where a file view publishes its menu actions. The menu reads it each time it
 * opens; a view clears it when it stops showing them.
 */
export type ArtifactMenuActions = MutableRefObject<ArtifactMenuAction[]>;
