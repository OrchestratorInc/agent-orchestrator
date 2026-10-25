import type { WebContents } from "electron";
import { SCROLL_PHASE_CHANNEL, type ScrollPhase } from "../shared/scroll-phase";

/**
 * Relays trackpad scroll begin/end to the renderer, which DOM wheel events
 * cannot report. Swipe navigation needs it to tell a held swipe from a
 * released one. Chromium ends a touchpad scroll about 100ms after the fingers
 * lift, or after momentum when it follows; the renderer handles momentum.
 */
export function relayScrollPhases(contents: WebContents): void {
	contents.on("input-event", (_event, input) => {
		let phase: ScrollPhase;
		if (input.type === "gestureScrollBegin") phase = "down";
		else if (input.type === "gestureScrollEnd") phase = "up";
		else return;
		if (!contents.isDestroyed()) contents.send(SCROLL_PHASE_CHANNEL, phase);
	});
}
