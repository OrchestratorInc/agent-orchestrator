import type { WebContents } from "electron";
import { SCROLL_PHASE_CHANNEL, type ScrollPhase } from "../shared/scroll-phase";

export function relayScrollPhases(contents: WebContents): void {
	contents.on("input-event", (_event, input) => {
		let phase: ScrollPhase;
		if (input.type === "gestureScrollBegin") phase = "down";
		else if (input.type === "gestureScrollEnd") phase = "up";
		else return;
		if (!contents.isDestroyed()) contents.send(SCROLL_PHASE_CHANNEL, phase);
	});
}
