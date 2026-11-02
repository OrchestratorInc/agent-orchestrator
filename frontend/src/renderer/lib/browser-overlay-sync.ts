import { BROWSER_OVERLAY_CANDIDATE_SELECTOR, OPEN_BROWSER_OVERLAY_SELECTOR } from "./dom-selectors";

// Whether an overlay is open is a fact about the whole document, and the main
// process keeps one shell stacking state. Several useBrowserView instances can
// be mounted at once (SessionView, BrowserPanel); if each tracked overlays
// separately, one unmounting would lower the shell under a dialog that another
// instance still saw as open. One shared, ref-counted observer keeps the raised
// state in step with the DOM for as long as any native browser is mounted.

let subscribers = 0;
let overlayOpen = false;
let observer: MutationObserver | null = null;

function sync(): void {
	const open = document.querySelector(OPEN_BROWSER_OVERLAY_SELECTOR) !== null;
	if (open === overlayOpen) return;
	overlayOpen = open;
	// The live page never moves or becomes a bitmap. Reordering the explicit
	// transparent shell is the complete overlay handoff for menus/dialogs.
	window.ao?.browser.setOverlayOpen(open);
}

const containsOverlayCandidate = (node: Node): boolean =>
	node instanceof Element &&
	(node.matches(BROWSER_OVERLAY_CANDIDATE_SELECTOR) || node.querySelector(BROWSER_OVERLAY_CANDIDATE_SELECTOR) !== null);

/** Keeps the native shell raised while any browser overlay is open. Returns an unsubscribe. */
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
		// Radix reuses its portal node and flips `data-state` in place rather than
		// adding/removing a body child, so a `childList`-only observer misses the
		// open/close transition under rapid toggling and the overlay state desyncs.
		// Watch subtree attribute flips on `data-state`, but inspect the mutation
		// target before querying open overlays so unrelated Radix state changes
		// don't trigger a document-wide selector scan.
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
