import { BROWSER_OVERLAY_CANDIDATE_SELECTOR, OPEN_BROWSER_OVERLAY_SELECTOR } from "./dom-selectors";

let subscribers = 0;
let overlayOpen = false;
let observer: MutationObserver | null = null;

function sync(): void {
	const open = document.querySelector(OPEN_BROWSER_OVERLAY_SELECTOR) !== null;
	if (open === overlayOpen) return;
	overlayOpen = open;
	window.ao?.browser.setOverlayOpen(open);
}

const containsOverlayCandidate = (node: Node): boolean =>
	node instanceof Element &&
	(node.matches(BROWSER_OVERLAY_CANDIDATE_SELECTOR) || node.querySelector(BROWSER_OVERLAY_CANDIDATE_SELECTOR) !== null);

export function subscribeBrowserOverlaySync(): () => void {
	subscribers += 1;
	if (subscribers === 1) {
		observer = new MutationObserver((mutations) => {
			const overlayChanged = mutations.some((mutation) => {
				if (mutation.type === "attributes") return containsOverlayCandidate(mutation.target);
				return [...mutation.addedNodes, ...mutation.removedNodes].some(containsOverlayCandidate);
			});
			if (overlayChanged) sync();
		});
		observer.observe(document.body, {
			childList: true,
			subtree: true,
			attributes: true,
			attributeFilter: ["data-state"],
		});
	}
	sync();
	let active = true;
	return () => {
		if (!active) return;
		active = false;
		subscribers -= 1;
		if (subscribers > 0) return;
		observer?.disconnect();
		observer = null;
		overlayOpen = false;
		window.ao?.browser.setOverlayOpen(false);
	};
}
