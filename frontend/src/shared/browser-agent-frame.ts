// The frame around a tab the agent works in. The page is a native view that
// paints above the app's own HTML, so nothing the app draws can sit on top of
// it without taking the person's clicks and landing in the agent's
// screenshots. Instead the view host shrinks an agent tab's page by these
// amounts and the Browser panel draws the frame in the room left around it:
// a thin edge on three sides and a strip along the bottom.
//
// The view host and the panel must agree on these numbers. They are CSS
// pixels: the host scales them by the page zoom like the slot it measures.

export const AGENT_FRAME_EDGE_PX = 6;
export const AGENT_FRAME_STRIP_PX = 24;

type Rect = { x: number; y: number; width: number; height: number };

/** The page's rect inside an agent tab's frame, or the slot unchanged when it
 *  is too small to frame. `scale` converts CSS pixels to the slot's units. */
export function insetForAgentFrame<T extends Rect>(rect: T, scale = 1): T {
	const factor = Number.isFinite(scale) && scale > 0 ? scale : 1;
	const edge = AGENT_FRAME_EDGE_PX * factor;
	const strip = AGENT_FRAME_STRIP_PX * factor;
	const width = rect.width - edge * 2;
	const height = rect.height - edge - strip;
	if (width < 1 || height < 1) return rect;
	return { ...rect, x: rect.x + edge, y: rect.y + edge, width, height };
}
