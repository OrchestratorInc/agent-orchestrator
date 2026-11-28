import { useEffect, useRef } from "react";
import { aoBridge } from "./bridge";

/**
 * Escape for a full-screen artifact viewer or an artifact preview, also from
 * inside its framed page. Keys pressed in a framed page never reach this
 * document, so its own Escape listeners cannot see them; while such a surface
 * is open, main reports Escape pressed anywhere in the shell instead.
 */

// Open surfaces, so one closing does not stop main reporting for another.
let armed = 0;

function arm() {
	armed += 1;
	if (armed === 1) aoBridge.artifactEscape.setArmed(true);
}

function disarm() {
	armed = Math.max(0, armed - 1);
	if (armed === 0) aoBridge.artifactEscape.setArmed(false);
}

/** Whether focus is in a framed page inside `container`, whose keys this document never sees. */
function framedPageFocused(container: Element): boolean {
	const active = document.activeElement;
	return (active instanceof HTMLIFrameElement || active?.tagName === "WEBVIEW") && container.contains(active);
}

/**
 * While `active`, calls `onEscape` when Escape is pressed with focus in a
 * framed page inside `container`. Escape with focus anywhere else is left to
 * this document's own listeners, so it is never handled twice; a menu or
 * dialog open over the page keeps focus in this document and keeps its Escape.
 */
export function useArtifactFrameEscape(active: boolean, container: () => Element | null, onEscape: () => void): void {
	const containerRef = useRef(container);
	containerRef.current = container;
	const onEscapeRef = useRef(onEscape);
	onEscapeRef.current = onEscape;
	useEffect(() => {
		if (!active) return;
		arm();
		const stop = aoBridge.artifactEscape.onEscape(() => {
			const element = containerRef.current();
			if (element && framedPageFocused(element)) onEscapeRef.current();
		});
		return () => {
			stop();
			disarm();
		};
	}, [active]);
}
