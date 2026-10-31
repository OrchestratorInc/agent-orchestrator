/** Longest a handoff waits for the next modal before the stand-in backdrop goes. */
const HANDOFF_TIMEOUT_MS = 250;

/**
 * Hands the screen from one modal to the next without the backdrop dipping.
 * Closing the first fades its backdrop out and opening the next fades a new
 * one in, so for a few frames the app shows through. While a handoff runs, a
 * stand-in backdrop holds the screen dim and backdrops skip their fades; the
 * stand-in leaves in the same frame the next modal's backdrop arrives (before
 * paint, since mutation callbacks run first), or after a timeout if no modal
 * opens. The arriving backdrop is marked so it never fades in later; it still
 * fades out when its modal closes.
 */
export function beginModalHandoff(): void {
	const root = document.documentElement;
	const standIn = document.createElement("div");
	standIn.className = "dialog-overlay";
	standIn.dataset.slot = "modal-handoff-backdrop";
	standIn.setAttribute("aria-hidden", "true");
	// A backdrop counts as open unless it is closing; the palette's own has no
	// state and leaves the DOM when it closes.
	const isOpen = (overlay: Element) => overlay.getAttribute("data-state") !== "closed";
	const openAtStart = new Set([...document.querySelectorAll(".dialog-overlay")].filter(isOpen));

	let finished = false;
	const finish = () => {
		if (finished) return;
		finished = true;
		observer.disconnect();
		window.clearTimeout(timer);
		standIn.remove();
		// Leave the fades off while the next backdrop first paints. A timer, not
		// a frame callback, so a window in the background still clears it.
		window.setTimeout(() => {
			if (!document.querySelector("[data-slot='modal-handoff-backdrop']")) delete root.dataset.modalHandoff;
		}, 50);
	};
	const observer = new MutationObserver(() => {
		for (const overlay of document.querySelectorAll(".dialog-overlay")) {
			// A new backdrop, or one still in the DOM from an earlier close that
			// the next modal reopens.
			if (overlay !== standIn && !openAtStart.has(overlay) && isOpen(overlay)) {
				// Its fade-in stays off while it is open: turning the fade back on
				// later would restart the animation from transparent.
				overlay.setAttribute("data-modal-handoff", "");
				finish();
				return;
			}
		}
	});
	const timer = window.setTimeout(finish, HANDOFF_TIMEOUT_MS);

	root.dataset.modalHandoff = "true";
	document.body.appendChild(standIn);
	observer.observe(document.body, { childList: true, subtree: true, attributes: true, attributeFilter: ["data-state"] });
}
