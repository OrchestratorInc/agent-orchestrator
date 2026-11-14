import { dragStyle } from "./PageTopbar";

export function BareTopbarDragStrip() {
	return dragStyle ? (
		<div aria-hidden="true" className="bare-topbar-drag-strip absolute inset-x-0 top-0 z-chrome h-3" data-testid="bare-topbar-drag-strip" style={dragStyle} />
	) : null;
}
