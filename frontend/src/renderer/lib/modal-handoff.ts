const HANDOFF_TIMEOUT_MS = 250;

export function beginModalHandoff(): void {
	const root = document.documentElement;
	const standIn = document.createElement("div");
	standIn.className = "dialog-overlay";
	standIn.dataset.slot = "modal-handoff-backdrop";
	standIn.setAttribute("aria-hidden", "true");
	const isOpen = (overlay: Element) => overlay.getAttribute("data-state") !== "closed";
	const openAtStart = new Set([...document.querySelectorAll(".dialog-overlay")].filter(isOpen));

	let finished = false;
	const finish = () => {
		if (finished) return;
		finished = true;
		observer.disconnect();
		window.clearTimeout(timer);
		standIn.remove();
		window.setTimeout(() => {
			if (!document.querySelector("[data-slot='modal-handoff-backdrop']")) delete root.dataset.modalHandoff;
		}, 50);
	};
	const observer = new MutationObserver(() => {
		for (const overlay of document.querySelectorAll(".dialog-overlay")) {
			if (overlay !== standIn && !openAtStart.has(overlay) && isOpen(overlay)) {
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
