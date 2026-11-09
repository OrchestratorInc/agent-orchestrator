import { dragStyle } from "./PageTopbar";

/**
 * An invisible macOS window-drag strip along the top edge of a page whose top
 * bar is gone. When the panel reclaims the top bar's band the page's own bar
 * (its usual drag region) is hidden, so this keeps the top edge draggable
 * without moving any content. It only shows in that state (styles.css), and the
 * panel's controls follow the page in the DOM so they stay clickable above it.
 * It is thin (not the full bar height) because a drag region swallows clicks on
 * whatever sits under it, and a page's own content now starts at the panel's edge.
 */
export function BareTopbarDragStrip() {
	return dragStyle ? (
		<div aria-hidden="true" className="bare-topbar-drag-strip absolute inset-x-0 top-0 z-chrome h-3" data-testid="bare-topbar-drag-strip" style={dragStyle} />
	) : null;
}
