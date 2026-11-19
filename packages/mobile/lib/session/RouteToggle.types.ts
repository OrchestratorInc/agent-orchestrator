export type RouteToggleProps = {
	/** On: the composer writes straight to the terminal. Off: it messages the agent. */
	on: boolean;
	onChange: (on: boolean) => void;
};

/**
 * What the toggle does, in the words screen readers use. The glyph is always
 * the terminal and the state is on or off, so the label never has to change
 * with the state, and the glyph never shows the mode the composer is not in.
 */
export const ROUTE_TOGGLE_LABEL = "Send to terminal";
